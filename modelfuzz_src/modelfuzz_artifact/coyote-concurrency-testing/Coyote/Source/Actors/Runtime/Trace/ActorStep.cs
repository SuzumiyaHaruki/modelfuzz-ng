// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

namespace Microsoft.Coyote.Actors
{
    internal class ActorStep : Step
    {
        public ActorId Actor;

        public ActorStep(string type, ActorId actor)
            : base(type)
        {
            this.Actor = actor;
        }
    }
}
