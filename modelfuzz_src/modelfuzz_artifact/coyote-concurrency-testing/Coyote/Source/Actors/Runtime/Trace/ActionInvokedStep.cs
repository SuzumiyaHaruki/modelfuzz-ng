// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

namespace Microsoft.Coyote.Actors
{
    internal class ActionInvokedStep : ActorStep
    {
        internal string InvokedAction { get; }

        public ActionInvokedStep(ActorId actor, string invokedAction)
            : base("InvokedAction", actor)
        {
            this.InvokedAction = invokedAction;
        }
    }
}
