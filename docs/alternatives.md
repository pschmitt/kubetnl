
# Alternatives

Since you are probably here in search for tools that allow you to forward traffic from within your cluster to the outside: here is a list of alternative tools that achieve similiar things as kubetnl and that might are what you are looking for:

## Telepresence

[Telepresence](https://github.com/telepresenceio/telepresence) will forward traffic from within your cluster to your local machine and also forward traffic from your local machine to the cluster. Also any environment variables and volume mounts (not sure about that though) will be available on your local machine once "connected". If you are looking to setup a local development environment for microservice development, Telepresense is probably the right choice. 

You still might choose `kubetnl` because:

With Telepresence, you need a to setup a Deployment manually before anything can be forwarded. Telepresence will then inject sidecar containers into the pods of that Deployment that are responsible for forwarding connections. `kubetnl` on the other hand creates a new service and pod for you, so there is no need to setup anything before the tunnel can be opened.

Telepresence will have to create a new namespace with a *Traffic manager* deployment (but setting it up and tearing it down is super easy) before anything can be forwarded. With `kubectl` there is no extra setup needed.

Telepresence forwards traffic from within the cluster only to your local machine, not to external endpoints (like `kubetnl` does).

## VSCode Bridge to Kubernetes

[VSCode Bridge to Kubernetes](https://docs.microsoft.com/en-us/visualstudio/bridge/bridge-to-kubernetes-vs-code) is quiet a similiar tool to Telepresense. 
It also allows you to forward traffic from your cluster to your local machine as well as the other way around making it a good environment for microservice development. 
For Brige to Kubernetes you need to have an existing pod (and service) set up. 
That pod gets replaced with a pod thats responsible for forwarding the traffic back and forth between the cluster and your local machine. 

However, this only works as a VS Code extension and external endpoints are not supported.
