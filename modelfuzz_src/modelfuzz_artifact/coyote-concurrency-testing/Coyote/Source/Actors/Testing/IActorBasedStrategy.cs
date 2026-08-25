// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using Microsoft.Coyote.Logging;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    internal interface IActorBasedStrategy
    {
        internal bool InitializeNextIteration(uint iteration, ActorExecutionTrace prevActorTrace, ExecutionTrace prevTrace);

        internal bool Finalize(LogWriter logWriter);
    }

    /// <summary>
    /// Guidance mode.
    /// </summary>
    public enum RunMode
    {
        /// <summary>
        /// Random exploration.
        /// </summary>
        RANDOM = 0,

        /// <summary>
        /// TLA+ state-guided exploration.
        /// </summary>
        STATE,

        /// <summary>
        /// Trace-guided exploration.
        /// </summary>
        TRACE
    }
}
