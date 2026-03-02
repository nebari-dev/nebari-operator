#!/usr/bin/env python3
"""
Authenticated WebAPI client for Nebari.

Modes:
  (default)   Single GET request to --path
  --explore   Walk every API endpoint: health, caller-identity, categories,
              services (flat list), service detail (by UID),
              notifications, pins — then pin and unpin to exercise the full cycle.
  --pin UID   Pin a service by UID
  --unpin UID Unpin a service by UID

Usage examples:
    # Single endpoint (default)
    python webapi_request.py -u admin -p secret -k

    # Explore all routes
    python webapi_request.py -u admin -p secret -k --explore

    # Pin / unpin a specific service
    python webapi_request.py -u admin -p secret -k --pin   06a32698-e1c6-4d28-b8a9-85db6f744c74
    python webapi_request.py -u admin -p secret -k --unpin 06a32698-e1c6-4d28-b8a9-85db6f744c74

    # Confidential client (password grant)
    python webapi_request.py -u admin -p secret -k \\
        --client-id nebari-system-webapi --client-secret <secret>

    # Service account (client credentials)
    python webapi_request.py --client-id my-client --client-secret xyz

Requires: pip install requests
"""

import argparse
import getpass
import json
import sys

import urllib3

try:
    import requests
except ImportError:
    sys.exit("Missing dependency: pip install requests")

KEYCLOAK_URL = "https://keycloak.nic-deploy.nebari.dev/auth"
WEBAPI_URL = "https://webapi.nic-deploy.nebari.dev"
DEFAULT_REALM = "nebari"
DEFAULT_PATH = "/api/v1/services"

# ── auth helpers ──────────────────────────────────────────────────────────────


def get_token_password(
    keycloak_url: str,
    realm: str,
    username: str,
    password: str,
    client_id: str = "webapi",
    client_secret: str | None = None,
    verify: bool = True,
) -> str:
    """Password grant. Pass client_secret for confidential clients."""
    token_url = f"{keycloak_url}/realms/{realm}/protocol/openid-connect/token"
    data: dict = {
        "grant_type": "password",
        "client_id": client_id,
        "username": username,
        "password": password,
    }
    if client_secret:
        data["client_secret"] = client_secret
    resp = requests.post(token_url, data=data, timeout=10, verify=verify)
    resp.raise_for_status()
    return resp.json()["access_token"]


def get_token_client_credentials(
    keycloak_url: str, realm: str, client_id: str, client_secret: str, verify: bool = True
) -> str:
    """Client credentials grant (service account)."""
    token_url = f"{keycloak_url}/realms/{realm}/protocol/openid-connect/token"
    resp = requests.post(
        token_url,
        data={
            "grant_type": "client_credentials",
            "client_id": client_id,
            "client_secret": client_secret,
        },
        timeout=10,
        verify=verify,
    )
    resp.raise_for_status()
    return resp.json()["access_token"]


# ── API helpers ───────────────────────────────────────────────────────────────


class APIClient:
    def __init__(self, base_url: str, token: str, verify: bool = True):
        self.base = base_url.rstrip("/")
        self.token = token
        self.verify = verify
        self._session = requests.Session()
        self._session.verify = verify
        self._session.headers["Authorization"] = f"Bearer {token}"

    def _url(self, path: str) -> str:
        return self.base + path

    def get(self, path: str) -> requests.Response:
        return self._session.get(self._url(path), timeout=10)

    def put(self, path: str) -> requests.Response:
        return self._session.put(self._url(path), timeout=10)

    def delete(self, path: str) -> requests.Response:
        return self._session.delete(self._url(path), timeout=10)


def _print_response(label: str, resp: requests.Response) -> None:
    ok = "✓" if resp.ok else "✗"
    print(f"  [{ok}] {label} → {resp.status_code}")
    if resp.content:
        try:
            print("    " + json.dumps(resp.json(), indent=2).replace("\n", "\n    "))
        except ValueError:
            print("   ", resp.text[:200])


def collect_services(resp: requests.Response) -> list[dict]:
    """Extract services from a flat /api/v1/services response."""
    try:
        body = resp.json()
    except ValueError:
        return []
    return body.get("services", [])


# ── modes ─────────────────────────────────────────────────────────────────────


def mode_single(client: APIClient, path: str) -> None:
    print(f"[api]  GET {client.base}{path}")
    resp = client.get(path)
    print(f"[api]  status: {resp.status_code}")
    try:
        print(json.dumps(resp.json(), indent=2))
    except ValueError:
        print(resp.text)
    if not resp.ok:
        sys.exit(1)


def mode_explore(client: APIClient) -> None:
    print("\n── 1. API health ───────────────────────────────────────────────")
    _print_response("GET /api/v1/health", client.get("/api/v1/health"))

    print("\n── 2. Caller identity ──────────────────────────────────────────")
    _print_response("GET /api/v1/caller-identity", client.get("/api/v1/caller-identity"))

    print("\n── 3. Categories ───────────────────────────────────────────────")
    _print_response("GET /api/v1/categories", client.get("/api/v1/categories"))

    print("\n── 4. Services list ────────────────────────────────────────────")
    svc_resp = client.get("/api/v1/services")
    _print_response("GET /api/v1/services", svc_resp)
    services = collect_services(svc_resp)

    print("\n── 5. Service detail (by UID) ─────────────────────────────────")
    if services:
        for svc in services:
            uid = svc.get("id", "")
            name = svc.get("name", "?")
            if uid:
                path = f"/api/v1/services/{uid}"
                _print_response(f"GET {path}  ('{name}')", client.get(path))
    else:
        print("  (no services in cache)")

    print("\n── 6. Notifications ───────────────────────────────────────────")
    _print_response("GET /api/v1/notifications", client.get("/api/v1/notifications"))

    print("\n── 7. Pins — read current pins ─────────────────────────────────")
    pins_resp = client.get("/api/v1/pins")
    _print_response("GET /api/v1/pins", pins_resp)

    print("\n── 8. Pins — pin/unpin cycle ───────────────────────────────────")
    if services:
        uid = services[0].get("id", "")
        name = services[0].get("name", "?")
        if uid:
            _print_response(f"PUT  /api/v1/pins/{uid}  (pin '{name}')", client.put(f"/api/v1/pins/{uid}"))
            _print_response("GET  /api/v1/pins (after pin)", client.get("/api/v1/pins"))
            _print_response(f"DELETE /api/v1/pins/{uid}  (unpin '{name}')", client.delete(f"/api/v1/pins/{uid}"))
            _print_response("GET  /api/v1/pins (after unpin)", client.get("/api/v1/pins"))
        else:
            print("  (service has no UID)")
    else:
        print("  (no services to pin)")

    print()


def mode_pin(client: APIClient, uid: str) -> None:
    path = f"/api/v1/pins/{uid}"
    print(f"[api]  PUT {client.base}{path}")
    resp = client.put(path)
    print(f"[api]  status: {resp.status_code}")
    if not resp.ok:
        print(resp.text)
        sys.exit(1)
    print("[pin]  pinned ✓")


def mode_unpin(client: APIClient, uid: str) -> None:
    path = f"/api/v1/pins/{uid}"
    print(f"[api]  DELETE {client.base}{path}")
    resp = client.delete(path)
    print(f"[api]  status: {resp.status_code}")
    if not resp.ok:
        print(resp.text)
        sys.exit(1)
    print("[pin]  unpinned ✓")


# ── main ──────────────────────────────────────────────────────────────────────


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Authenticated WebAPI client",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "--keycloak-url", default=KEYCLOAK_URL, help=f"Keycloak base URL with /auth (default: {KEYCLOAK_URL})"
    )
    parser.add_argument("--webapi-url", default=WEBAPI_URL, help=f"WebAPI base URL (default: {WEBAPI_URL})")
    parser.add_argument("--realm", default=DEFAULT_REALM, help=f"Keycloak realm (default: {DEFAULT_REALM})")
    parser.add_argument(
        "--path", default=DEFAULT_PATH, help=f"API path for single-request mode (default: {DEFAULT_PATH})"
    )

    mode_group = parser.add_mutually_exclusive_group()
    mode_group.add_argument(
        "--explore", action="store_true", help="Walk all API endpoints and exercise pin/unpin cycle"
    )
    mode_group.add_argument("--pin", metavar="UID", help="Pin a service by its UID")
    mode_group.add_argument("--unpin", metavar="UID", help="Unpin a service by its UID")

    cred_group = parser.add_argument_group("user credentials (password grant)")
    cred_group.add_argument("-u", "--username", help="Keycloak username (selects password grant)")
    cred_group.add_argument("-p", "--password", help="Keycloak password")

    client_group = parser.add_argument_group(
        "client identity",
        "If -u/-p are given → password grant. " "If only --client-secret is given → client credentials grant.",
    )
    client_group.add_argument("--client-id", default="webapi", help="OIDC client ID (default: webapi)")
    client_group.add_argument("--client-secret", help="OIDC client secret (required for confidential clients)")

    parser.add_argument(
        "-k", "--no-verify-ssl", action="store_true", default=False, help="Disable SSL certificate verification"
    )

    args = parser.parse_args()

    if args.no_verify_ssl:
        urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)
        print("[warn]  SSL verification disabled")

    verify = not args.no_verify_ssl

    # ── obtain token ──
    try:
        if args.username or args.password:
            username = args.username or input("Username: ")
            password = args.password or getpass.getpass("Password: ")
            hint = " + client secret" if args.client_secret else ""
            print(f"[auth] password grant — user: {username}, client: {args.client_id}{hint}")
            token = get_token_password(
                args.keycloak_url,
                args.realm,
                username,
                password,
                client_id=args.client_id,
                client_secret=args.client_secret,
                verify=verify,
            )
        elif args.client_secret:
            print(f"[auth] client credentials grant — client: {args.client_id}")
            token = get_token_client_credentials(
                args.keycloak_url,
                args.realm,
                args.client_id,
                args.client_secret,
                verify=verify,
            )
        else:
            username = input("Username: ")
            password = getpass.getpass("Password: ")
            print(f"[auth] password grant — user: {username}, client: {args.client_id}")
            token = get_token_password(
                args.keycloak_url,
                args.realm,
                username,
                password,
                client_id=args.client_id,
                verify=verify,
            )
    except requests.HTTPError as exc:
        sys.exit(f"[error] Keycloak token request failed: {exc.response.status_code} {exc.response.text}")

    print("[auth] token obtained ✓")

    client = APIClient(args.webapi_url, token, verify=verify)

    # ── dispatch mode ──
    try:
        if args.explore:
            mode_explore(client)
        elif args.pin:
            mode_pin(client, args.pin)
        elif args.unpin:
            mode_unpin(client, args.unpin)
        else:
            mode_single(client, args.path)
    except requests.RequestException as exc:
        sys.exit(f"[error] Request failed: {exc}")


if __name__ == "__main__":
    main()
