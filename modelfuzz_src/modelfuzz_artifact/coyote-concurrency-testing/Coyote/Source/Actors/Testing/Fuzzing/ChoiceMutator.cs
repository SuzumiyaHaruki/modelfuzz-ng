// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    internal sealed class ChoiceMutator : IMutator
    {
        private System.Random RandomGenerator;
        public ChoiceMutator()
        {
            this.RandomGenerator = new System.Random(Guid.NewGuid().GetHashCode());
        }

        public ExecutionTrace Mutate(ActorExecutionTrace t, ExecutionTrace ex, out ActorExecutionTrace at)
        {
            List<int> choiceIndices = new List<int>();
            for (int i = 0; i < ex.Length; i++)
            {
                if (ex[i].Kind == ExecutionTrace.DecisionKind.NondeterministicChoice && ex[i].BooleanChoice.HasValue)
                {
                    choiceIndices.Add(i);
                }
            }

            int toMutateIndex = this.RandomGenerator.Next(choiceIndices.Count);
            bool choice = ex[choiceIndices[toMutateIndex]].BooleanChoice.Value;
            ex[choiceIndices[toMutateIndex]].BooleanChoice = !choice;
            at = t;
            return ex;
        }
    }
}
