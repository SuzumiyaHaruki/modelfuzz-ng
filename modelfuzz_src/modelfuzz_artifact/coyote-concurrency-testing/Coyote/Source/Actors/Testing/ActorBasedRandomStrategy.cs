// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System.Collections.Generic;
using System.Linq;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    internal class ActorBasedRandomStrategy : ActorBasedInterleavingStrategy, IActorBasedStrategy
    {
        internal ActorBasedRandomStrategy(Configuration configuration, bool isFair = true)
            : base(configuration, isFair)
        {
        }

        internal override bool NextOperation(IEnumerable<ControlledOperation> ops, ControlledOperation current,
            bool isYielding, out ControlledOperation next)
        {
            int idx = this.RandomValueGenerator.Next(2000 * ops.Count()) % ops.Count();
            next = ops.ElementAt(idx);
            return true;
        }

        /// <inheritdoc/>
        internal override bool NextBoolean(ControlledOperation current, out bool next)
        {
            next = this.RandomValueGenerator.Next(2) is 0 ? true : false;
            return true;
        }

        /// <inheritdoc/>
        internal override bool NextInteger(ControlledOperation current, int maxValue, out int next)
        {
            next = this.RandomValueGenerator.Next(maxValue);
            return true;
        }

        /// <inheritdoc/>
        internal override string GetName() => "ActorBasedRandomStrategy";

        /// <inheritdoc/>
        internal override string GetDescription() => $"{this.GetName()}[seed:{this.RandomValueGenerator.Seed}]";
    }
}
