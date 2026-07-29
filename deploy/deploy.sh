#!/bin/bash

# Deploy Ergon to Kubernetes cluster

echo "Building Docker image..."
docker build -t ergon .

echo "Pushing to registry (update with your registry)..."
# docker push your-registry/ergon:latest

echo "Deploying to Kubernetes..."
kubectl apply -f deploy/k8s/pvcs.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/ingress.yaml

echo "Deployment complete!"
echo "Check status with: kubectl get pods, services, ingress"