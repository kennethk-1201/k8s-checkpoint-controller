# k8s-checkpoint-controller
A controller to manage pod migration between nodes.

It depends on node-to-node communication to transfer checkpoint archives. As of now, we will depend on [k8s- checkpoint-api](https://github.com/kennethk-1201/k8s-checkpoint-api) to handle this aspect.

For setting up the cluster, refer to this [custom setup script](https://github.com/kennethk-1201/kubeadm-scripts/tree/main) for setting up a multi-VM cluster with checkpointing enabled.