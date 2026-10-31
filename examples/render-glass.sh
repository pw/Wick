#!/bin/sh
# render-glass.sh W H SPP OUT.png [CAUSTIC-GRID] — glass.wick on every core, stitched.
set -e
W=$1; H=$2; SPP=$3; OUT=$4; GRID=$5
cd "$(dirname "$0")/.."
N=$(nproc); TMP=$(mktemp -d)
i=0
while [ $i -lt $N ]; do
  r0=$(( H * i / N )); r1=$(( H * (i + 1) / N ))
  ./wick examples/glass.wick $W $H $r0 $r1 $SPP $TMP/band$(printf %03d $i) $GRID &
  i=$((i + 1))
done
wait
{ printf 'P3\n%s %s\n255\n' $W $H; cat $TMP/band*; } > $TMP/all.ppm
convert $TMP/all.ppm "$OUT"
rm -rf $TMP
echo "wrote $OUT"
