// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

namespace Microsoft.Coyote.Actors
{
    internal class StateTransitionStep : ActorStep
    {
        public string FromState;

        public string ToState;

        public StateTransitionStep(ActorId actor, string from, string to)
            : base("StateTransition", actor)
        {
            this.FromState = from;
            this.ToState = to;
        }
    }
}
