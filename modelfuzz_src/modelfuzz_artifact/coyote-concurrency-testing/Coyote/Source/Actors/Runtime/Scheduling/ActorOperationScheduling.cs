// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using Microsoft.Coyote.Logging;
using Microsoft.Coyote.Runtime;
using Microsoft.Coyote.Testing;
using Microsoft.Coyote.Testing.Interleaving;
using RandomInterleavingStrategy = Microsoft.Coyote.Testing.Interleaving.RandomStrategy;

namespace Microsoft.Coyote.Actors
{
    internal class ActorOperationScheduler : OperationScheduler
    {
        internal ActorExecutionTrace ActorExecutionTrace { get; set; }

        protected ActorOperationScheduler(Configuration configuration, SchedulingPolicy policy, IRandomValueGenerator generator, ExecutionTrace prefixTrace)
            : base(configuration, policy, generator, prefixTrace)
        {
            if (this.SchedulingPolicy is SchedulingPolicy.Interleaving)
            {
                this.Portfolio.RemoveLast();
                switch (configuration.ExplorationStrategy)
                {
                    case ExplorationStrategy.InterleavedFuzzing:
                        this.Portfolio.AddLast(new InterleavedFuzzingStrategy(configuration, generator));
                        break;
                    case ExplorationStrategy.InterleavedRoundRobinFuzzing:
                        this.Portfolio.AddLast(new InterleavedRoundRobinStrategy(configuration, generator));
                        break;
                    case ExplorationStrategy.QLearningTLA:
                        this.Portfolio.AddLast(new InterleavedQLearningStrategy(configuration));
                        break;
                    case ExplorationStrategy.ActorBasedDFS:
                        this.Portfolio.AddLast(new ActorBasedDFSStrategy(configuration));
                        break;
                    default:
                        this.Portfolio.AddLast(new RandomInterleavingStrategy(configuration));
                        break;
                }

                foreach (var strategy in this.Portfolio)
                {
                    strategy.RandomValueGenerator = generator;
                    if (strategy is InterleavingStrategy interleavingStrategy)
                    {
                        interleavingStrategy.TracePrefix = prefixTrace;
                    }
                }
            }
        }

        internal static new ActorOperationScheduler Setup(Configuration configuration, ExecutionTrace prefixTrace) =>
            new ActorOperationScheduler(configuration,
                configuration.IsSystematicFuzzingEnabled ? SchedulingPolicy.Fuzzing : SchedulingPolicy.Interleaving,
                new RandomValueGenerator(configuration), prefixTrace);

        /// <summary>
        /// Creates a new instance of the <see cref="OperationScheduler"/> class.
        /// </summary>
        internal static new ActorOperationScheduler Setup(Configuration configuration, SchedulingPolicy policy,
            IRandomValueGenerator valueGenerator) =>
            new ActorOperationScheduler(configuration, policy, valueGenerator, ExecutionTrace.Create());

        // TODO: override initialize next iteration
        internal new bool InitializeNextIteration(uint iteration, LogWriter logWriter)
        {
            if (iteration > 0)
            {
                // Rotate the portfolio strategies using round-robin.
                var strategy = this.Portfolio.First.Value;
                this.Portfolio.RemoveFirst();
                this.Portfolio.AddLast(strategy);
            }

            bool result = false;
            this.Strategy.LogWriter = logWriter;
            if (this.Strategy is IActorBasedStrategy)
            {
                IActorBasedStrategy strategy = this.Strategy as IActorBasedStrategy;
                result = strategy.InitializeNextIteration(iteration, this.ActorExecutionTrace, this.Trace);
            }
            else
            {
                result = this.Strategy.InitializeNextIteration(iteration);
            }

            if (iteration > 0)
            {
                this.Trace.Clear();
            }

            return result;
        }

        internal bool Finalize(LogWriter logWriter)
        {
            if (this.Strategy is IActorBasedStrategy)
            {
                IActorBasedStrategy strategy = this.Strategy as IActorBasedStrategy;
                return strategy.Finalize(logWriter);
            }

            return false;
        }
    }
}
