// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using Microsoft.Coyote.Logging;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    internal interface IMutator
    {
        ExecutionTrace Mutate(ActorExecutionTrace t, ExecutionTrace ex, out ActorExecutionTrace at);
    }

    internal static class MutatorFactory
    {
        internal static IMutator FromName(string name, IRandomValueGenerator rand, Configuration configuration) => name switch
        {
            "choice" => new ChoiceMutator(),
            "process" => new ProcessSequenceMutator(rand),
            "delay" => new DelayMutator(configuration.NumDelays, rand),
            "actor" => new ActorSwapMutator(rand),
            _ => new EmptyMutator()
        };
    }
}
