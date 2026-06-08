# Kubernetes manifests

## Local development with [kind](https://kind.sigs.k8s.io/)

From the repository root:

1. **Create a cluster** (optional if you already have one named `gateway-dev`):

   ```bash
   kind create cluster --config k8s/kind-cluster.yaml
   ```

2. **Build the image** (tag must match `image` in `deployment.yaml`):

   ```bash
   docker build -t victornguyen247/llm-gateway:v0.1 .
   ```

3. **Load the image into kind** (nodes pull from the Docker daemon, not a registry):

   ```bash
   kind load docker-image victornguyen247/llm-gateway:v0.1 --name gateway-dev
   ```

4. **Apply manifests** (create `gateway-secret` with `OPENAI_API_KEY` first if it does not exist):

   ```bash
   kubectl apply -f k8s/
   ```

`imagePullPolicy: IfNotPresent` in the Deployment is appropriate for this flow: kind uses the pre-loaded image and does not need a registry pull.

## Cloud clusters

On EKS, GKE, AKS, or similar: **push** the image to a registry the cluster can reach, reference that image in the Deployment, add **`imagePullSecrets`** if the registry is private, and change the Service **`type`** from `ClusterIP` to **`LoadBalancer`** (or front the Service with an Ingress / Gateway API resource) so traffic can reach the gateway from outside the cluster.
