#!/bin/sh
# Walks personas through the contract sample apps and prints what each got.
# Runs inside the cluster, so the in-cluster Keycloak URLs resolve.
#
# Usage: sh probe.sh <gateway-ip> <password> <spec>...
# Each spec is app|host|persona|username|login-path|claims-path|path,path,...
# with username "-" for an anonymous persona and claims-path "-" for none.
#
# Output, one line each:
#   CELL <app> <persona> <path> <outcome>   outcome: login, <status> or <status>:<location>
#   CLAIMS <app> <persona> <response body of claims-path>
#   ERROR <app> <persona> <message>
set -u
GW="$1"
PASSWORD="$2"
shift 2
OUT=/tmp/probe
rm -rf "$OUT"
mkdir -p "$OUT"

# A redirect to the Keycloak authorization endpoint is reported as "login",
# whichever client started it.
outcome() {
  case "$1" in
    301 | 302 | 303 | 307 | 308)
      case "$2" in
        */protocol/openid-connect/auth*) echo login ;;
        *) echo "$1:$2" ;;
      esac
      ;;
    *) echo "$1" ;;
  esac
}

probe() {
  app=$1 host=$2 persona=$3 username=$4 login=$5 claims=$6 paths=$7
  jar="$OUT/$app.$persona.jar"
  out="$OUT/$app.$persona.out"
  page="$OUT/$app.$persona.page"
  : >"$jar"
  c() { curl -sk --max-time 30 -c "$jar" -b "$jar" --resolve "$host:443:$GW" "$@"; }

  if [ "$username" != "-" ]; then
    # Follow the app's redirects to the Keycloak sign-in form, submit it, and
    # follow the callback back to the app, as a browser would.
    c -L -o "$page" "https://$host$login"
    action=$(sed -n 's/.*action="\([^"]*\)".*/\1/p' "$page" | head -n 1 | sed 's/&amp;/\&/g')
    if [ -z "$action" ]; then
      echo "ERROR $app $persona no sign-in form after following $login" >>"$out"
      return
    fi
    landed=$(c -L -o /dev/null -w '%{url_effective}' \
      --data-urlencode "username=$username" --data-urlencode "password=$PASSWORD" "$action")
    case "$landed" in
      */protocol/openid-connect/* | */login-actions/*)
        echo "ERROR $app $persona sign-in did not complete, still at $landed" >>"$out"
        return
        ;;
    esac
  fi

  old_ifs=$IFS
  IFS=,
  for path in $paths; do
    IFS=$old_ifs
    result=$(c -o /dev/null -w '%{http_code} %{redirect_url}' "https://$host$path")
    echo "CELL $app $persona $path $(outcome ${result% *} "${result#* }")" >>"$out"
  done
  IFS=$old_ifs

  if [ "$claims" != "-" ]; then
    echo "CLAIMS $app $persona $(c "https://$host$claims")" >>"$out"
  fi
}

for spec in "$@"; do
  old_ifs=$IFS
  IFS='|'
  # shellcheck disable=SC2086 # split the spec on |
  set -- $spec
  IFS=$old_ifs
  probe "$@" &
done
wait
cat "$OUT"/*.out
