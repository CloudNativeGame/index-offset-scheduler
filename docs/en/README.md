# index-offset-scheduler

[中文](./docs/en/README.md) | English

## Overview
index-offset-scheduler is a custom kubernetes scheduler. It allows pods belonging to the same GameServerSet to be deployed in a rolling manner according to the pod index number, so that the offset/difference of the pod index on each node is as large as possible.
For games with rolling server launch, this is a common demand scenario:
- Before the game goes online, due to a large number of reservations or traffic, more game services need to be prepared in advance. In the k8s environment, the id of the game service
  is generally related to the pod number.
- After the server is launched, the load of the pod corresponding to the game server will increase significantly and cause the load of the machine to increase significantly. If multiple adjacent pods are located on the same node,
  for example, pod-1 and pod-2, the rapid increase in the load of the pod in a short period of time may require the node configuration to be prepared according to the highest resources required by the two pods. After a period of time, as the number of active players decreases, the load of the machine will decrease again.
- Therefore, in the scenario of traditional machine deployment, the common practice is to prepare multiple machines in advance and deploy the game service in a rolling deployment manner, so that the node can deploy more services and will not face the risk of a single machine carrying the highest load of multiple game servers in the short term. The deployment effect is similar to the distribution below:
```txt
node1: game-1, game-4...
node2: game-2, game-5...
node3: game-3, game-6...
```
The default k8s scheduler will ensure that the pods are distributed as evenly as possible, but it cannot ensure that the difference between the pod numbers on each node is as large as possible, and even two pods with consecutive index numbers may be distributed on the same node.
Therefore, it is necessary to implement a custom scheduler to make the offset/difference of the index of the pod on each node as large as possible.

## Deployment
If you have installed [OpenKruiseGame](https://openkruise.io/zh/kruisegame/introduction/), install it directly through the following command:
```
# If you have not deployed OKG and want to test the effect through the StatefulSet type application, you need to create a namespace in advance: kruise-game-system
# kubectl create namespace kruise-game-system
kubectl apply -f deploy/index-offset-scheduler.yaml
```

## Usage example
The sample test-gss.yaml is provided in the deploy directory, which you can use to test the effect of OpenKruiseGame's GameServerSet.
The key point is to specify pod.spec.schedulerName as index-offset-scheduler.
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
### Note
- Custom scheduler is an extension of scoring logic. Other built-in scheduler plugins will still work normally, so scoring may not completely affect the final binding result. You can refer to
  [KubeSchedulerConfiguration](https://kubernetes.io/zh-cn/docs/reference/scheduling/config/) to configure the startup parameters of the scheduler, and control the final scheduling effect by configuring the plugin
  to enable or not and weight. For example you can increase the weight of index-offset-scheduler to 5:
 ```yaml
 #configmap
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
- Compatibility: [Scheduler Configuration](https://kubernetes.io/docs/reference/scheduling/config/) is already in stable state in Kubernetes version 1.25, and has been tested on clusters of versions 1.25 and 1.31 to work properly.

### Working principle
This document shows how the custom scheduler works: [Configure Multiple Schedulers](https://kubernetes.io/docs/tasks/extend-kubernetes/configure-multiple-schedulers/).
The core working principle of index-offset-scheduler is as follows:
Suppose there are 3 nodes in the cluster: n1, n2, n3. At this time, there is no pod of the specified GameServerSet on each node. And the status of these 3 nodes is consistent, that is, the scores of other default plugins are consistent.
```
n1: []
n2: []
n3: []
````
1. When pod-0 is created for scheduling, since there is no pod (GameServer) belonging to the specified GameServerSet on each node,
   each node scores 100, and the pod may be scheduled to any node.
2. If pod-0 is bound to n2, when pod-1 is created for scheduling, the scores of n1 and n3 are 100. The scoring logic of n2 is to find the maximum value on n2 that is less than the current index number to be scheduled, and use the difference as the score,
   that is, 1=1-0.
3. If pod-1 is bound to n3, pod-2 will be bound to n1. Now the pod scheduling will be as follows:
```
n1: pod-2
n2: pod-0
n3: pod-1
```
4. According to the scoring rules above, if pod-3~pod-5 are scheduled, the final scheduler situation is as follows:
```
n1: pod-2, pod-5
n2: pod-0, pod-3
n3: pod-1, pod-4
```
When you rebuild pod-2, the scoring is as follows:
```
n1: 5 (when there is a pod on the node, but the smallest pod index number is also greater than the current number, the smallest index number on the node is used as the difference)
n2: 2=2-0
n3: 1=2-1
```
pod-2 will be rebound to n1.
5. Some special examples: If you have more than 100 nodes, or you use the Open Kruise Game framework to skip many numbers, the pod distribution is as follows:
```
n1: pod-2, pod-105
n2: pod-4, pod-107
n3: pod-7, pod-108
```
If you delete pod-4, the final score is as follows:
```
n1: 2=4-2
n2: 107
n3: 7
```
The scores of all nodes will be normalized to ensure that they are less than 100:
```
n1: 1 = 2/2
n2: 53 = 107/2
n3: 3 = 7/2
```
## Experimental features:
- index-offset-scheduler provides support for pods that are not managed by StatefulSet/GameServerSet. If you manage multiple pods by writing multiple pod configurations and keep the pod naming in the StatefulSet style, you can achieve a similar effect by adding an annotation with the key index-offset-scheduler/scheduler-selector and the value pod labels to the pod annotation.

### Development Guide
The core scoring logic is located in pkg/scheduler.go. If you want to improve the logic:
```
git clone <repo>
# add your code
go mod tidy
docker build -t index-offset-scheduler:latest -f Dockerfile .
# If the network is not good, please add GOPROXY to the compilation statement in dockerfile
```
The following documents can be used for reference:
- https://kubernetes.io/docs/tasks/extend-kubernetes/configure-multiple-schedulers/
- https://github.com/cnych/sample-scheduler-framework/blob/master/main.go
- https://medium.com/@juliorenner123/k8s-creating-a-kube-scheduler-plugin-8a826c486a1
- https://overcast.blog/creating-a-custom-scheduler-in-kubernetes-a-practical-guide-2d9f9254f3b5