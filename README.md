# index-offset-scheduler

中文 | [English](./docs/en/README.md)

## 概述
index-offset-scheduler是一个自定义的kubernetes调度器。它可以让属于同一个GameServerSet的pod按照pod的索引编号滚动部署，使得每个节点上的
pod的索引的偏移量/差值尽可能得大。  
对于滚动开服类的游戏，这是常见的需求场景：
- 在游戏上线前，由于存在大量预约或者导量，需要提前准备较多的游戏服务。在k8s环境中，游戏服务的id
  一般与pod编号存在对应关系。
- 开服后，游戏服对应的pod负载会明显上升并导致所在机器的负载明显上升，如果临近的多个pod位于同一个node上，
  例如pod-1和pod-2，短期内的pod的负载快速上升可能导致需要按照两个pod的所需的最高资源准备node配置。而一段时间后，随着活跃玩家的下降，机器的负载又会下降。
- 因此在传统机器部署的场景下，常见的做法是：提前准备多台机器，将游戏服务按照滚动部署的方式部署，以使得节点能够部署较多的服务，并且不会面临短期内
  单台机器承载多个游戏服的最高负载的风险。部署效果类似下方的分布:
    ```txt
    node1: game-1, game-4...
    node2: game-2, game-5...
    node3: game-3, game-6...
    ```
默认的k8s调度器会保障pod尽可能均匀的分布，但是不能确保每个节点上pod之间的编号差值尽可能大，甚至可能出现连续索引编号的两个pod分布在同一个节点上。
因此需要实现一个自定义调度器，使得每个节点上的 pod的索引的偏移量/差值尽可能尽可能得大。

## 部署
如果你安装了[OpenKruiseGame](https://openkruise.io/zh/kruisegame/introduction/)，直接通过下面的命令安装:
```
# 如果你没有部署OKG，而希望通过StatefulSet类型的应用测试效果，你需要提前创建namespace: kruise-game-system
# kubectl create namespace kruise-game-system
kubectl apply -f deploy/index-offset-scheduler.yaml
```

## 使用示例
deploy目录下提供了示例test-gss.yaml，你分别可以用来测试OpenKruiseGame的GameServerSet的效果。  
关键点在于指定pod.spec.schedulerName为index-offset-scheduler。
```yaml
apiVersion: game.kruise.io/v1alpha1
kind: GameServerSet
metadata:
  name: test
  namespace: kube-system
spec:
  serviceName: ""
  replicas: 10
  gameServerTemplate:
    metadata:
      labels:
        app: test
    spec:
      schedulerName: index-offset-scheduler
      containers:
        - name: test
          image: nginx
```
### 说明
- 自定义调度器是对打分逻辑的扩展，内置的其他调度器插件仍然会正常工作，因此打分不一定会完全影响到最终的绑定结果。你可以参考
  [KubeSchedulerConfiguration](https://kubernetes.io/zh-cn/docs/reference/scheduling/config/)配置调度器的启动参数，通过配置插件
  的启用与否和权重来控制最终的调度效果。例如你可以将index-offset-scheduler的权重增加至5:
    ```yaml
    # configmap
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: index-offset-scheduler-config
      namespace: kruise-game-system
    data:
      scheduler-config.yaml: |
        # stable v1 after version 1.25
        apiVersion: kubescheduler.config.k8s.io/v1
        kind: KubeSchedulerConfiguration
        leaderElection:
          leaderElect: false
          resourceNamespace: kruise-game-system
          resourceName: index-offset-scheduler
        profiles:
          - schedulerName: index-offset-scheduler
            plugins:
              score:
                enabled:
                  - name: index-offset-scheduler
                    weight: 5
    ```
- 兼容性: [Scheduler Configuration](https://kubernetes.io/docs/reference/scheduling/config/) 在kubernetes的1.25版本已经处于stable状态，已经在1.25和1.31版本的集群上测试可以正常工作。


### 工作原理
这个文档展示了自定义调度器的工作原理: [Configure Multiple Schedulers](https://kubernetes.io/docs/tasks/extend-kubernetes/configure-multiple-schedulers/)。
index-offset-scheduler的核心工作原理如下:   
假如集群中有3个节点: n1、n2、n3。此时每个节点上都没有指定的GameServerSet的pod。并且这3个节点的状态一致，即其他默认插件的打分一致。
```  
n1: []   
n2: []   
n3: []
````
1、当pod-0被创建待调度时，由于每个节点上都没有属于指定的GameServerSet的pod(GameServer)，
所以每个节点得分为100，pod可能被调度到任意一个节点。  
2、如果pod-0倍绑定到n2，当pod-1被创建待待调度时，n1、n3的得分是100。n2的打分逻辑为，找出n2上小于当前待调度索引编号的最大值，将差值作为得分，
即1=1-0。   
3、如果pod-1被绑定到n3，pod-2将被绑定到n1。现在pod的调度情况将如下所示:
```
n1: pod-2   
n2: pod-0    
n3: pod-1   
```
4、按照上方的打分规则，如果pod-3~pod-5被调度, 最终的调度器情况如下所示:
```
n1: pod-2, pod-5  
n2: pod-0, pod-3  
n3: pod-1, pod-4  
```
当你重建pod-2时，打分情况如下:
``` 
n1: 5(当节点上存在pod，但是最小的pod索引编号也大于当前编号，那么以节点上的最小索引编号作为差值)  
n2: 2=2-0 
n3: 1=2-1
```
pod-2将会被重新绑定至n1。
5、一些特别的例子: 如果你的节点超过了100个，或者你使用了Open Kruise Game框架跳过了许多编号，使得pod的分布如下:
```
n1: pod-2, pod-105  
n2: pod-4, pod-107  
n3: pod-7, pod-108  
```
如果你删除了pod-4，最终的得分如下:
``` 
n1: 2=4-2
n2: 107
n3: 7
```  
所有节点的打分最终会被归一化处理以确保小于100:
```
n1: 1 = 2/2  
n2: 53 = 107/2  
n3: 3 = 7/2  
```
## 实验性功能:
- index-offset-scheduler提供了对于不通过StatefulSet/GameServerSet管理的pod的支持。如果你通过编写多份pod的配置管理多个pod，并且保持
  pod的命名符合StatefulSet的风格。通过在pod的注解中添加key为index-offset-scheduler/scheduler-selector，值为pod的labels的注解，可以
  实现类似的效果。

### 开发指引
核心的打分逻辑位于pkg/scheduler.go，如果你想要改进逻辑:
```
git clone <repo>
# add your code
go mod tidy
docker build -t index-offset-scheduler:latest  -f Dockerfile .
# 如果网络情况不佳，请在dockerfile中的编译语句中添加GOPROXY
```
下方文档可供参考:
- https://kubernetes.io/zh-cn/docs/tasks/extend-kubernetes/configure-multiple-schedulers/
- https://arthurchiao.art/blog/k8s-scheduling-plugins-zh/
- https://juejin.cn/post/7427399875236528191#heading-27
- https://blog.csdn.net/weixin_43845924/article/details/138451208