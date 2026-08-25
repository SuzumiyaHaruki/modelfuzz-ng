// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    internal class ProcessSequenceMutator : IMutator
    {
        private IRandomValueGenerator rand;

        public ProcessSequenceMutator(IRandomValueGenerator rand)
        {
            this.rand = rand;
        }

        public ExecutionTrace Mutate(ActorExecutionTrace t, ExecutionTrace ex, out ActorExecutionTrace at)
        {
            // Currently only swap positions of operations
            int one = this.rand.Next(ex.Length);
            int two = this.rand.Next(ex.Length);
            at = t;
            return SwapPositions(ex, one, two);
        }

        protected static ExecutionTrace SwapPositions(ExecutionTrace t, int one, int two)
        {
            ExecutionTrace.Step temp = t[one];
            t[one] = t[two];
            t[two] = temp;
            return t;
        }
    }
}
