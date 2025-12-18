# nic-operator

Kubernetes Operator designed to streamline and centralize the configuration of **routing**, **TLS certificates**, and
**SSO authentication** within the NIC ecosystem.

This project targets a GitOps-friendly platform where:
- **Argo CD** deploys application Helm charts (the “workloads/apps”)
- [NS/EW traffic management](https://devcookies.medium.com/north-south-vs-east-west-traffic-in-microservices-a-complete-guide-0e458fe4e605):
  - **Envoy Gateway (Gateway API)** provides north/south traffic entry
  - **Istio** provides mesh capabilities (east/west, optional policies)
- **cert-manager** provisions/renews TLS certificates
- **Keycloak** provides authentication / user & client management

The operator’s purpose is to enable self-service onboarding: > “When a new app is installed via Helm/Argo CD, the
platform automatically wires DNS/TLS, routes, and SSO.”


## Goals

- Provide a single onboarding __contract__ for apps (via a CRD or annotation-based intent).
- Automatically reconcile:
  - **Gateway API routes** (e.g., `HTTPRoute`)
  - **TLS** (cert-manager driven)
  - **SSO/OIDC enforcement** at the edge (Envoy Gateway policies)
  - **Keycloak client provisioning** for each onboarded app
- Be **GitOps-compatible**:
  - Users/app charts define intent
  - Operator renders/owns generated platform resources
  - Changes are declarative and continuously reconciled

```mermaid
flowchart TB
  Helm[Helm install app] --> K8s[(Kubernetes API)]
  K8s --> AppCR[NicApp CR]

  subgraph Operator[nic-operator]
    D[Intent Reconciler]
    R[Routing Reconciler]
    T[TLS Reconciler]
    A[Auth Reconciler]
    K[Keycloak Reconciler optional]
  end

  AppCR --> D
  D --> R --> HTTPRoute[HTTPRoute]
  HTTPRoute --> Gateway[Gateway]

  D --> T --> Certs[cert-manager Certificate or shared wildcard secret]
  Certs --> Gateway

  D --> A --> SecPol[EnvoyGateway SecurityPolicy OIDC]
  Secret[OIDC client secret in K8s Secret] --> SecPol

  D --> K --> KC[Keycloak client]
  KC --> Secret

```

See the Architectural decision issue for more information. [soon]
