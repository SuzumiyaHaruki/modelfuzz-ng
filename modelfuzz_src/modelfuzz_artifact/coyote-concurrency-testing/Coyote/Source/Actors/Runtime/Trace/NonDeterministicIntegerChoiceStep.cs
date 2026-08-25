// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

namespace Microsoft.Coyote.Actors
{
    internal class NonDeterministicIntegerChoiceStep : Step
    {
        public int Choice;

        public NonDeterministicIntegerChoiceStep(int choice)
            : base("NonDeterministicIntegerChoice")
        {
            this.Choice = choice;
        }
    }
}
