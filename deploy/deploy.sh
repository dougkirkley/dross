#!/bin/bash

# Deploy Dross to Kubernetes cluster

echo "Building Docker image..."
docker build -t dross .

echo "Pushing to registry (update with your registry)..."
# docker push your-registry/dross:latest

echo "Deploying to Kubernetes..."
kubectl apply -f deploy/k8s/pvcs.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/ingress.yaml

echo "Deployment complete!"
echo "Check status with: kubectl get pods, services, ingress"