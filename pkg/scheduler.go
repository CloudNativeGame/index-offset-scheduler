package scheduler

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	PluginName    = "index-offset-scheduler"
	AnnotationKey = "index-offset-scheduler/scheduler-selector"
)

type CustomScheduler struct {
	handle framework.Handle
}

var _ = framework.ScorePlugin(&CustomScheduler{})

func New(ctx context.Context, configuration runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	// 插件初始化逻辑
	return &CustomScheduler{handle: handle}, nil
}

func (p *CustomScheduler) Name() string {
	return PluginName
}

func (p *CustomScheduler) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	klog.Infof("Now Scoring NodeName: %s", nodeName)
	nodeInfo, err := p.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 1, framework.AsStatus(fmt.Errorf("getting node %q from Snapshot: %v", nodeName, err))
	}

	currentPodIndex, err := getPodIndex(pod)
	if err != nil {
		return 1, framework.AsStatus(fmt.Errorf("invalid annotation format for pod %s: %v", pod.Name, err))
	}
	// 判断是否存在AnnotationKey，如果不存在，则通过pod的ownerReference来匹配.
	selector, ok := pod.Annotations[AnnotationKey]
	// AnnotationKey格式为k1=v1，如果是多个，使用","连接
	// 提取成selector列表，用于匹配，提取和匹配逻辑在matchesSelector函数中实现
	/*
		一般情况下，通过pod的索引让pod滚动分布，调度的对象和比较的pod对象拥有相同的lables，因此无需指定注解标识要比较的pod的特征。
		考虑一些极特殊的需求，例如通过此调度器，让game服务滚动分布了，同时希望让相同编号的mail服务也滚动分布，此时需要通过pod的注解标识来指定mail服务pod的特征。
		当然上面这个例子，最好的做法是将game服务和mail服务放在同一个pod中。
		这个需求更可能出现在手动管理一个个pod的情况，即pod不属于任何statefulset对象。在比较时，会尝试比较labels和namespace。请尽量不要这么做，如果你有跳过某些索引编号的需求，可以查看下open kruise game这个项目。
	*/
	var nodePodIndexs []int
	if ok {
		// 添加正则判断：selector格式是否符合：k1=v1，如果是多个，使用","连接
		re, _ := regexp.Compile(`^[a-zA-Z0-9]+=[a-zA-Z0-9]+(,[a-zA-Z0-9]+=[a-zA-Z0-9]+)*$`)
		if !re.MatchString(selector) {
			return 1, framework.NewStatus(framework.Error, fmt.Sprintf("selector format error: %s", selector))
		}
		for _, podInfo := range nodeInfo.Pods {
			p := podInfo.Pod
			if p.Namespace != pod.Namespace {
				continue
			}
			if matchesSelector(p, selector) {
				nodePodIndex, err := getPodIndex(p)
				if err != nil {
					continue
				}
				nodePodIndexs = append(nodePodIndexs, nodePodIndex)
			}
		}
	} else {
		// 当前pod是否属于某个statefulset。
		// 当前调度器不支持pod属于多个controller的情况。
		if pod.OwnerReferences == nil || len(pod.OwnerReferences) != 1 {
			klog.Infof("pod %s is not belong to any workload or pod is belong to mutil workload", pod.Name)
			return 1, framework.NewStatus(framework.Error, fmt.Sprintf("pod %s is not belong to any statefulset", pod.Name))
		}
		if pod.OwnerReferences[0].Kind != "StatefulSet" {
			klog.Infof("pod %s is not belong to any statefulset", pod.Name)
			return 1, framework.NewStatus(framework.Error, fmt.Sprintf("pod %s is not belong to any statefulset", pod.Name))
		}
		for _, podInfo := range nodeInfo.Pods {
			if compareOwnerReferences(pod, podInfo.Pod) {
				nodePodIndex, err := getPodIndex(podInfo.Pod)
				debugLog("%s is  belong to the same workload of %s", podInfo.Pod.Name, pod.Name)
				if err != nil {
					continue
				}
				nodePodIndexs = append(nodePodIndexs, nodePodIndex)
			} else {
				debugLog("%s is not belong to the same workload of %s", podInfo.Pod.Name, pod.Name)
			}
		}
	}

	// 打分逻辑
	// 情况1:节点上面没有符合条件的pod，得分为0，这是一种特殊情况。最终在归一化函数中得分将会最大。
	// 情况2：节点上面有符合条件的pod，并且存在小于待调度pod的变好的pod，取节点上小于当前pod的pod的最大值，使用当前pod的编号减去最大小于当前pod的编号，作为得分，如果最终有大于100的，在归一化函数中处理。
	// 情况3: 节点上面有符合条件的pod，但是最小的pod大于待调度pod，取节点上最小的pod编号作为得分。如果最终结果大于100，在归一化函数中处理。

	// 旧逻辑：
	// 1、如果大于当前待调度的pod，则向下取小于待调度的pod的最大值，如果一直向下取到没有则视为没有pod，直接打分100
	// 2、pod上面有符合条件的pod，并根据上面的规则获取最大编号，计算差值。同时获取所有节点上的最大编号的差值的最大值，以此为分母，当前节点差值为分子。乘以50进行归一化处理。
	var diff int
	if len(nodePodIndexs) == 0 {
		// 记录日志
		klog.Infof("No pod in nodeName: %s is empty, its score is 0, and it's best", nodeName)
		// 记录日志由于为空，打分100
		return 0, nil
	} else {
		sort.Ints(nodePodIndexs)
		maxLessThanCurrent := -1
		for _, index := range nodePodIndexs {
			if index < currentPodIndex && index > maxLessThanCurrent {
				maxLessThanCurrent = index
				//break
			}
		}
		//如果当前节点没有符合条件的pod，取当前pod的编号作为得分
		if maxLessThanCurrent == -1 {
			klog.Infof("nodeName: %s is not empty, but min nodePodIndexs: %v still greater than currentIndex, its score will be  min nodePodIndexs", nodeName, nodePodIndexs)
			// 返回pods中的最小值
			return int64(nodePodIndexs[0]), nil
		} else {
			// 如果有符合条件的pod，取差值
			diff = currentPodIndex - maxLessThanCurrent
			klog.Infof("nodeName: %s is not empty,its score will be currentPodIndex(%v) - maxLessThanCurrent(%v)", nodeName, currentPodIndex, maxLessThanCurrent)
			return int64(diff), nil
			// allMaxDiff := p.getMaxDiff(pod)
			// return int64(diff / allMaxDiff * 50), nil
		}
	}

}

func (p *CustomScheduler) ScoreExtensions() framework.ScoreExtensions {
	return p
}

// NormalizeScore implements framework.ScoreExtensions
func (*CustomScheduler) NormalizeScore(ctx context.Context, state *framework.CycleState, p *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	klog.Infof("NormalizeScore is called, Now NodeScoreList: %v", scores)
	// 在此处对分数进行归一化处理，如果分数为0，最后变为最大值100。
	// 获取所有不为0的分数，取最大的分数
	// 逐渐测试一个压缩比例，使得最大的分数能压缩到0-100之间
	// 将这个比例应用到所有不为100的分数上
	maxDiff := int64(0)
	for _, nodeScore := range scores {
		if nodeScore.Score > maxDiff {
			maxDiff = nodeScore.Score
		}
	}
	if maxDiff == 0 {
		return nil
	}
	compress := int64(1)
	for maxDiff > 100 {
		compress = compress * 2
		maxDiff = maxDiff / compress
	}
	debugLog("NormalizeScore is called, maxDiff: %v, compress: %v", maxDiff, compress)
	for i, NodeScore := range scores {
		if NodeScore.Score == 0 {
			scores[i].Score = 100
		} else {
			scores[i].Score = int64(NodeScore.Score / compress)
			debugLog("After Normalize, NodeScore %s is : %v %v", scores[i].Name, scores[i].Score, NodeScore.Score)
		}
	}
	klog.Infof("NormalizeScore is called, After Normalize, NodeScoreList: %v", scores)
	return nil
}

func getPodIndex(pod *v1.Pod) (int, error) {
	// 从pod-name中提取pod的序号
	if pod.Name == "" {
		return 0, fmt.Errorf("pod name is empty")
	}
	// 使用分割符号提取pod的序号
	parts := strings.Split(pod.Name, "-")
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid pod name format")
	}
	indexStr := parts[len(parts)-1]

	if indexStr == "" {
		return 0, fmt.Errorf("pod num is empty")
	}
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		return 0, fmt.Errorf("annotation value is not a index: %s", indexStr)
	}
	return index, nil
}

func matchesSelector(pod *v1.Pod, selector string) bool {
	podLabels := pod.Labels
	selectorParts := strings.Split(selector, ",")
	for _, part := range selectorParts {
		kv := strings.Split(part, "=")
		if len(kv) != 2 {
			continue
		}
		key, value := kv[0], kv[1]
		// 判断key是否存在以及值是否相等
		if _, ok := podLabels[key]; !ok {
			return false
		}
		if podLabels[key] != value {
			return false
		}
	}
	return true
}

// 判断pod的namespace、ownerReferences中的kind和name是否与selector中的namespace、ownerReferences中的kind和name匹配
func compareOwnerReferences(pod *v1.Pod, nodePod *v1.Pod) bool {
	if pod.Namespace != nodePod.Namespace {
		debugLog("%s's podNamespace: %s is not equal to nodePod %s's Namespace: %s", pod.Name, pod.Namespace, nodePod.Name, nodePod.Namespace)
		return false
	}
	if nodePod.OwnerReferences == nil || len(nodePod.OwnerReferences) != 1 {
		debugLog("%s's podOwnerReferences of Node's pod is empty or len is not 1", nodePod.Name)
		return false
	}
	if nodePod.OwnerReferences[0].Kind != "StatefulSet" {
		debugLog("%s's podOwnerReferences of Node's pod is not StatefulSet", nodePod.Name)
	}
	if pod.OwnerReferences[0].Name != nodePod.OwnerReferences[0].Name || string(pod.OwnerReferences[0].UID) != string(nodePod.OwnerReferences[0].UID) {
		debugLog("%s's podOwnerReferences of pod: %s %s is not equal to OwnerReferences of Node %s's pod: %s %s ", pod.Name, pod.OwnerReferences[0].Name, pod.OwnerReferences[0].UID, nodePod.Name, nodePod.OwnerReferences[0].Name, nodePod.OwnerReferences[0].UID)
		return false
	}
	return true
}

// 自定义日志打印方法，当klog.v(6)时才打印用于调试
func debugLog(format string, args ...interface{}) {
	if klog.V(6).Enabled() {
		klog.Infof(format, args...)
	}
}
