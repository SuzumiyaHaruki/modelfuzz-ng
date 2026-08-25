#!/bin/bash

# Binary name
BINARY="./2pc-fuzzing"

# Parameters with defaults
NUM_EXPERIMENTS=${1:-20}   # Default: 20 experiments
DURATION=${2:-60}          # Default: 60 minutes

# Fuzzer types and corresponding output filenames
TYPES=("Modelfuzz" "Random" "Trace" "Line" "RL")
FILENAMES=("mf_stats" "rnd_stats" "trc_stats" "line_stats" "rl_stats")

# Loop over each type
for i in "${!TYPES[@]}"; do
  TYPE="${TYPES[$i]}"
  FILENAME="${FILENAMES[$i]}"

  echo "Running $NUM_EXPERIMENTS experiments for type: $TYPE (duration: $DURATION minutes)"

  for ((j=0; j<NUM_EXPERIMENTS; j++)); do
    OFFSET=$j
    echo "  ➤ Experiment $((j+1)) (offset=$OFFSET)"
    $BINARY -type="$TYPE" -offset="$OFFSET" -filename="$FILENAME" -duration="$DURATION"
  done
done
