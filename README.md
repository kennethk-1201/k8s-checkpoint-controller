# k8s-checkpoint-controller
A controller to manage stateful pod migration between nodes. This controller is dependent on changes to the kubelet, which can be viewed in the forked repo:
* https://github.com/kennethk-1201/k8s-checkpoint-controller

For setting up the cluster to test this feature, refer to this [custom setup script](https://github.com/kennethk-1201/kubeadm-scripts/tree/main) for setting up a multi-VM cluster with checkpointing enabled.