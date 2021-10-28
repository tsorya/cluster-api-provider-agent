# cluster-api-provider-agent
Kubernetes-native declarative infrastructure for agent based installation

cluster-api-provider-agent serves as infrastructure provider for [Kubernetes cluster-api](https://github.com/kubernetes-sigs/cluster-api)

## How to install cluster-api-provider-agent

cluster-api-provider-agent is deployed into an existing OpenShift / Kubernetes cluster.

**Prerequisites:**
* Admin access to the OpenShift / Kubernetes cluster specified by the KUBECONFIG environment variable

Build the cluster-api-provider-agent image and push it to a container image repository:
```shell
make docker-build docker-push IMG=<your docker repository>:`git log -1 --short`
```

Deploy cluster-api-provider-agent to your cluster:
```shell
make deploy IMG=<your docker repository>:`git log -1 --short`
```
