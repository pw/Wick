#!/bin/sh
# caustic-glass.sh PHOTONS-PER-CORE OUT.txt — the photon pass on every core,
# averaged into one grid that render-glass.sh can read.
set -e
P=$1; OUT=$2
cd "$(dirname "$0")/.."
N=$(nproc); TMP=$(mktemp -d)
i=0
while [ $i -lt $N ]; do
  ./wick examples/glass.wick caustic $P $i - - $TMP/g$i &
  i=$((i + 1))
done
wait
# each run normalised its photons as if it were the only one: average them
paste -d' ' $TMP/g* | awk -v n=$N '{ m = NF / n; line = "";
  for (c = 1; c <= m; c++) { s = 0; for (k = 0; k < n; k++) s += $(c + k * m); line = line (c > 1 ? " " : "") s / n }
  print line }' > "$OUT"
rm -rf $TMP
echo "wrote $OUT"
