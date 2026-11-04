#!/bin/sh
# Sends requests through the gateway at the same time and prints one line
# per request:
#   RESULT <label> <HTTP status> <seconds> <body lines> [redirect URL]
# Runs inside the cluster, in the spec's curl pod.
# Usage: sh timed-requests.sh <gateway-ip> <cookie-jar or -> <label> <host> <path> [<label> <host> <path> ...]
set -u
GW="$1"
JAR="$2"
shift 2
COOKIES=""
if [ "$JAR" != "-" ]; then
  COOKIES="-b $JAR"
fi
while [ $# -ge 3 ]; do
  (
    BODY="/tmp/body.$1"
    rm -f "$BODY"
    # shellcheck disable=SC2086 # COOKIES is empty or two words on purpose.
    OUT=$(curl -sk $COOKIES -o "$BODY" --max-time 120 \
      -w '%{http_code} %{time_total} %{redirect_url}' \
      --resolve "$2:443:$GW" "https://$2$3")
    LINES=$(cat "$BODY" 2>/dev/null | wc -l | tr -d ' ')
    set -- "$1" $OUT
    echo "RESULT $1 $2 $3 $LINES ${4:-}"
  ) &
  shift 3
done
wait
