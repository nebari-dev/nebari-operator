#!/usr/bin/env bash

# Quick setup script for testing sample apps

set -euo pipefail

echo "🚀 Deploying sample applications..."
echo ""

echo "1️⃣  Deploying nginx-demo application..."
kubectl apply -f "$(dirname "$0")/nginx-demo.yaml"

echo ""
echo "2️⃣  Creating HTTPRoute (manual, until NicApp CRD is ready)..."
kubectl apply -f "$(dirname "$0")/nginx-demo-nicapp.yaml"

echo ""
echo "✅ Sample applications deployed!"
echo ""
echo "📋 Accessing your applications:"
echo ""
echo "1. Setup gateway access (macOS only, run once):"
echo "   ./dev/nic-dev gateway:setup"
echo ""
echo "2. Add hostname to /etc/hosts:"
echo "   echo '127.0.0.1  nginx-demo.nic.local' | sudo tee -a /etc/hosts"
echo ""
echo "3. Access the application:"
echo "   curl http://nginx-demo.nic.local"
echo "   or open http://nginx-demo.nic.local in your browser"
echo ""
echo "Check status:"
echo "  kubectl get nicapp -n demo-app"
echo "  kubectl get httproute -n demo-app"
echo "  kubectl get pods -n demo-app"
