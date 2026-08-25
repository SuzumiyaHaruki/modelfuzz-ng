// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

namespace Microsoft.Coyote.Actors
{
    internal class NonDeterministicBooleanChoiceStep : Step
    {
        public bool Choice;

        public NonDeterministicBooleanChoiceStep(bool choice)
            : base("NonDeterministicChoice")
        {
            this.Choice = choice;
        }
    }
}
