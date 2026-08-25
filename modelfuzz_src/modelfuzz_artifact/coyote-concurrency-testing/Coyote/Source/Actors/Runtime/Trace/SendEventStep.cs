// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

namespace Microsoft.Coyote.Actors
{
    internal class SendEventStep : ActorStep
    {
        internal ActorId Receiver { get; }

        internal Event Event { get; }

        public SendEventStep(Event e, ActorId sender, ActorId receiver)
            : base("SendEvent", sender)
        {
            this.Receiver = receiver;
            this.Event = e;
        }
    }
}
