// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    internal class ActorSwapMutator : IMutator
    {
        private IRandomValueGenerator rand;

        public ActorSwapMutator(IRandomValueGenerator rand)
        {
            this.rand = rand;
        }

        public ExecutionTrace Mutate(ActorExecutionTrace t, ExecutionTrace ex, out ActorExecutionTrace at)
        {
            // Currently only swap positions of operations
            int one;
            int two;
            Tuple<ActorStep, ActorStep> actors = this.GenerateActors(t, out one, out two);
            while (actors.Item1.Actor.Value == actors.Item2.Actor.Value)
            {
                actors = this.GenerateActors(t, out one, out two);
            }

            at = SwapPositions(t, one, two);
            return ex;
        }

        protected static ActorExecutionTrace SwapPositions(ActorExecutionTrace t, int one, int two)
        {
            ActorStep temp = (ActorStep)t[one];
            t[one] = t[two];
            t[two] = temp;
            return t;
        }

        protected Tuple<ActorStep, ActorStep> GenerateActors(ActorExecutionTrace t, out int one, out int two)
        {
            int one_ = this.rand.Next(t.Count);
            int two_ = this.rand.Next(t.Count);

            Step s1 = t[one_];
            Step s2 = t[two_];
            bool isActor = false;
            if ((s1.Type == "SendEvent" || s1.Type == "InvokedAction" || s1.Type == "ReceiveEvent") && (s2.Type == "SendEvent" || s2.Type == "InvokedAction" || s2.Type == "ReceiveEvent"))
            {
                isActor = true;
            }

            while (!isActor)
            {
                one_ = this.rand.Next(t.Count);
                two_ = this.rand.Next(t.Count);

                s1 = t[one_];
                s2 = t[two_];

                if ((s1.Type == "SendEvent" || s1.Type == "InvokedAction" || s1.Type == "ReceiveEvent") && (s2.Type == "SendEvent" || s2.Type == "InvokedAction" || s2.Type == "ReceiveEvent"))
                {
                    isActor = true;
                }
            }

            one = one_;
            two = two_;
            return new Tuple<ActorStep, ActorStep>((ActorStep)s1, (ActorStep)s2);
        }
    }
}
