// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System.Text.Json.Nodes;

namespace Microsoft.Coyote.Actors
{
    internal class Step
    {
        public string Type;

        public Step(string type)
        {
            this.Type = type;
        }
    }
}
