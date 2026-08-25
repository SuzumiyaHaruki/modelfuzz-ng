// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    internal sealed class DelayMutator : IMutator
    {
        private uint NumDelays { get; set; }

        private IRandomValueGenerator RandomGenerator;
        public DelayMutator(uint numDelays, IRandomValueGenerator rand)
        {
            this.NumDelays = numDelays;
            this.RandomGenerator = rand;
        }

        public ExecutionTrace Mutate(ActorExecutionTrace t, ExecutionTrace ex, out ActorExecutionTrace at)
        {
            t.RemoveAll(IsDelay);
            int index = -1;
            for (int i = 0; i < this.NumDelays; i++)
            {
                do
                {
                    int j = this.RandomGenerator.Next(t.Count);
                    index = (t[j].Type == "SendEvent" || t[j].Type == "InvokedAction") ? j : -1;
                }
                while (index < 0);

                t.Insert(index, new DelayStep());
            }

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

        private static bool IsDelay(Step s)
        {
            return s.Type == "Delay";
        }
    }
}
