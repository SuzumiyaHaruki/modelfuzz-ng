// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

namespace Microsoft.Coyote.Actors
{
    internal class ReceiveEventStep : ActorStep
    {
        public ActorId Sender;

        public Event Event;

        public ReceiveEventStep(Event e, ActorId sender, ActorId receiver)
            : base("ReceiveEvent", receiver)
        {
            this.Sender = sender;
            this.Event = e;
        }
    }
}
