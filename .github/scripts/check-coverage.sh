#!/usr/bin/env bash
set -euo pipefail

awk '
  NR == 1 {
    if ($0 !~ /^mode: (set|count|atomic)$/) { invalid = 1; exit }
    next
  }
  NF != 3 || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/ {
    invalid = 1
    exit
  }
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
