// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Linq;
using Microsoft.Coyote.Logging;
using Microsoft.Coyote.Runtime;
using Microsoft.Coyote.Testing.Interleaving;

namespace Microsoft.Coyote.Actors
{
    /// <summary>
    /// Abstract exploration strategy used during controlled testing.
    /// </summary>
    internal abstract class ActorBasedInterleavingStrategy : InterleavingStrategy, IActorBasedStrategy
    {
        /// <summary>
        /// Initializes a new instance of the <see cref="ActorBasedInterleavingStrategy"/> class.
        /// </summary>
        protected ActorBasedInterleavingStrategy(Configuration configuration, bool isFair)
            : base(configuration, isFair)
        {
        }

        bool IActorBasedStrategy.InitializeNextIteration(uint iteration, ActorExecutionTrace trace, ExecutionTrace ex)
        {
            return this.InitializeNextIteration(iteration);
        }

        bool IActorBasedStrategy.Finalize(LogWriter logWriter)
        {
            return false;
        }
    }
}
