#!/bin/sh
# Publish a realistic retained topic hierarchy against the dev broker.
set -e
H=mosquitto
sleep 2

pub() { mosquitto_pub -h "$H" -r -t "$1" -m "$2"; }

for room in livingroom kitchen bedroom hallway; do
  pub "home/$room/temperature" "$(awk -v s=$$ 'BEGIN{srand(s);printf "%.1f", 18+rand()*6}')"
  pub "home/$room/humidity"    "$(awk -v s=$$ 'BEGIN{srand(s+1);printf "%.0f", 35+rand()*25}')"
  pub "home/$room/light/state" "OFF"
done

for i in $(seq 1 40); do
  pub "devices/sensor-$i/state"  '{"online":true,"rssi":-62,"fw":"1.4.2"}'
  pub "devices/sensor-$i/uptime" "$((i * 137))"
done

for line in line1 line2; do
  for m in 1 2 3; do
    pub "factory/$line/machine$m/status"     "running"
    pub "factory/$line/machine$m/rpm"        "$((1200 + m * 37))"
    pub "factory/$line/machine$m/temperature" "$((60 + m * 3))"
  done
done

echo "seeded"
