#!/usr/bin/env bash
set -euo pipefail

# The root main.go only wires signals to cli.Run and ends in os.Exit, which an
# in-process test cannot cover without terminating the test runner; it is the
# sole file excluded from the 100% statement requirement.

awk '
  NR == 1 {
    if ($0 !~ /^mode: (set|count|atomic)$/) { invalid = 1; exit }
    next
  }
  NF != 3 || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/ {
    invalid = 1
    exit
  }
  $1 ~ /^github\.com\/dceoy\/oandactl\/main\.go:/ { next }
  {
    total += $2
    if ($3 > 0) covered += $2
  }
  END {
    if (invalid || total == 0) {
      print "Invalid or empty Go coverage profile" > "/dev/stderr"
      exit 2
    }
    printf "Statement coverage: %.2f%% (%d/%d)\n", 100 * covered / total, covered, total
    if (covered != total) {
      print "Statement coverage must be 100%" > "/dev/stderr"
      exit 1
    }
  }
' "${1:-coverage.out}"
