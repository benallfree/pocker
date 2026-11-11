#!/bin/bash
# Benchmarks one page of one app. usage: run.sh <label> <port> <path> <pocketbase binary> <app dir>
# <app dir> holds pb_data (with a superuser already created) and pb_pages or pb_hooks. Needs hey
# (https://github.com/rakyll/hey). The server runs from <app dir>, as 0.x needs for node_modules. Results land next
# to this script: <label>.c50.txt, <label>.c1.txt, <label>.log; one summary line goes to stdout.
set -u
label=$1 port=$2 path=$3 bin=$4 dir=$5
B=$(cd "$(dirname "$0")" && pwd)
(cd "$dir" && exec "$bin" serve --dir=./pb_data --http=127.0.0.1:$port >"$B/$label.log" 2>&1) &
pid=$!
for i in $(seq 1 50); do curl -s -m1 -o /dev/null "http://127.0.0.1:$port/api/health" && break; sleep 0.2; done
code=$(curl -s -m5 -o "$B/$label.html" -w '%{http_code}' "http://127.0.0.1:$port$path")
hey -n 500 -c 10 "http://127.0.0.1:$port$path" >/dev/null 2>&1   # warm up
hey -z 10s -c 50 "http://127.0.0.1:$port$path" >"$B/$label.c50.txt" 2>&1
hey -n 2000 -c 1 "http://127.0.0.1:$port$path" >"$B/$label.c1.txt" 2>&1
rss=$(ps -o rss= -p $pid | tr -d ' ')
kill $pid; wait $pid 2>/dev/null
rps=$(awk '/Requests\/sec/{print $2}' "$B/$label.c50.txt")
p50=$(awk '/ 50%% in/{print $3}' "$B/$label.c50.txt"); p99=$(awk '/ 99%% in/{print $3}' "$B/$label.c50.txt")
c1p50=$(awk '/ 50%% in/{print $3}' "$B/$label.c1.txt"); c1rps=$(awk '/Requests\/sec/{print $2}' "$B/$label.c1.txt")
non200=$(grep -A3 'Status code distribution' "$B/$label.c50.txt" | grep -v '\[200\]' | grep '\[' | tr -s ' ' | tr '\n' ';')
echo "$label code=$code rps=$rps p50=${p50}s p99=${p99}s c1_rps=$c1rps c1_p50=${c1p50}s rss_kb=$rss bytes=$(wc -c <"$B/$label.html") other=$non200"
