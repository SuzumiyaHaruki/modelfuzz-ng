// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    internal sealed class EmptyMutator : IMutator
    {
        public EmptyMutator()
        {
        }

        public ExecutionTrace Mutate(ActorExecutionTrace t, ExecutionTrace ex, out ActorExecutionTrace at)
        {
            at = null;
            return null;
        }
    }
}
