#!/bin/bash

# Parameters with defaults
NUM_EXPERIMENTS=${1:-20}
M=${2:-6}
N=${3:-40}
ITERATIONS=${4:-10000}

# Fuzzer types and corresponding output filenames
TYPES=("0" "1" "2")
declare -A TYPE_NAMES=(
  ["0"]="Random"
  ["1"]="ModelFuzz"
  ["2"]="Trace"
)

# Loop over each type
for i in "${!TYPES[@]}"; do
  TYPE="${TYPES[$i]}"
  type_name="${TYPE_NAMES[$TYPE]}"

  echo "Running $NUM_EXPERIMENTS experiments for $type_name"

  for ((j=0; j<NUM_EXPERIMENTS; j++)); do
    OFFSET=$j
    echo "  ➤ Experiment $((j+1)) (offset=$OFFSET)"
    dotnet run --framework net7.0 -- "$TYPE" "$M" "$N" "$OFFSET" "$ITERATIONS"
  done
done
