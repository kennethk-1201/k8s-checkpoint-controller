# k8s-checkpoint-controller
A controller to manage pod migration between nodes.

It depends on node-to-node communication to transfer checkpoint archive. As of now, we will depend on [k8s- checkpoint-api](https://github.com/kennethk-1201/k8s-checkpoint-api) to handle this aspect.