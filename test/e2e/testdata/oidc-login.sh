#!/bin/sh
# Logs in through the gateway's OIDC flow and prints RESULT=<final HTTP status code>.
# Runs inside the cluster so the in-cluster Keycloak URLs resolve.
# Usage: sh oidc-login.sh <hostname> <gateway-ip> <username> <password> [mode] [cookie-name] [cookie-value]
# Modes:
#   (none)         print the status of the authenticated request
#   print-cookie   print the value of <cookie-name> after login instead of a status
#   forge          replace the value of <cookie-name> with <cookie-value> after login,
#                  keep every other cookie, then print the status of the request
set -eu
HOST="$1"
GW="$2"
USERNAME="$3"
PASSWORD="$4"
MODE="${5:-}"
CNAME="${6:-}"
CVALUE="${7:-}"
JAR=/tmp/cookies
PAGE=/tmp/page
C="curl -sk -c $JAR -b $JAR --resolve ${HOST}:443:${GW}"

# 1. Unauthenticated request: the gateway redirects to the Keycloak login page.
LOGIN_URL=$($C -o /dev/null -w '%{redirect_url}' "https://${HOST}/")

# 2. Fetch the login form and extract its action URL.
$C -o "$PAGE" "$LOGIN_URL"
ACTION=$(sed -n 's/.*action="\([^"]*\)".*/\1/p' "$PAGE" | head -n 1 | sed 's/&amp;/\&/g')

# 3. Submit credentials. Keycloak redirects to the gateway's OAuth2 callback.
CALLBACK=$($C -o /dev/null -w '%{redirect_url}' \
  --data-urlencode "username=${USERNAME}" \
  --data-urlencode "password=${PASSWORD}" \
  "$ACTION")

# 4. The callback exchanges the code and sets the session cookies.
$C -o /dev/null "$CALLBACK"

# Cookie jar lines are tab separated with the value in the last field.
if [ "$MODE" = "print-cookie" ]; then
  printf 'RESULT='
  awk -F'\t' -v n="$CNAME" '$6 == n { printf "%s", $7 }' "$JAR"
  echo
  exit 0
fi
if [ "$MODE" = "forge" ]; then
  awk -F'\t' -v OFS='\t' -v n="$CNAME" -v v="$CVALUE" '$6 == n { $7 = v } { print }' "$JAR" > "$JAR.new"
  mv "$JAR.new" "$JAR"
fi

# 5. Authenticated request.
$C -o /dev/null -w 'RESULT=%{http_code}\n' "https://${HOST}/"
