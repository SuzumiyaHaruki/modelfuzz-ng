// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Text;
using CsvHelper;
using Microsoft.Coyote.Logging;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    /// <summary>
    /// A fuzzing strategy class.
    /// </summary>
    internal class InterleavedFuzzingStrategy : ActorBasedRandomStrategy, IActorBasedStrategy
    {
        internal IMutator Mutator;

        internal TLCClient TlcClient;

        protected ExecutionTrace CurTrace;

        internal HashSet<long> States;

        internal HashSet<string> UniqueTraces;

        protected Queue<Tuple<ActorExecutionTrace, ExecutionTrace>> TraceQueue;

        internal Queue<bool> BooleanChoices;

        internal Queue<int> IntegerChoices;

        internal RunMode Mode;

        internal List<int> StateHistory;

        internal List<string> ActualStates;

        internal string CsvPath;

        /// <summary>
        /// Initializes a new instance of the <see cref="InterleavedFuzzingStrategy"/> class.
        /// </summary>
        public InterleavedFuzzingStrategy(Configuration configuration, IRandomValueGenerator randomValueGenerator)
            : base(configuration)
        {
            this.TlcClient = new TLCClient(configuration.IndexOffset);
            this.States = new HashSet<long>();
            this.UniqueTraces = new HashSet<string>();
            this.TraceQueue = new Queue<Tuple<ActorExecutionTrace, ExecutionTrace>>();
            this.CurTrace = null;
            this.Mutator = MutatorFactory.FromName(configuration.MutatorType, randomValueGenerator, configuration);
            this.BooleanChoices = new Queue<bool>();
            this.IntegerChoices = new Queue<int>();
            this.StateHistory = new List<int>();
            this.StateHistory.Add(0);
            this.CsvPath = configuration.OutputFilePath;
            this.Mode = (RunMode)configuration.RunMode;
            this.ActualStates = new List<string>();
        }

        bool IActorBasedStrategy.InitializeNextIteration(uint iteration, ActorExecutionTrace actorTrace, ExecutionTrace trace)
        {
            // this.LogWriter.LogImportant($"Initializing iteration: {iteration + 1}");
            this.StepCount = 0;
            this.TlcClient.LogWriter = this.LogWriter;

            if (iteration == 0 || trace == null)
            {
                this.CurTrace = null;
                return true;
            }

            List<TLCState> states = this.TlcClient.SendTrace(actorTrace, true);
            int newStateCount = 0;
            int newTraceCount = 0;
            // string traceString = string.Empty;
            string traceString = TraceGenerator.GetTrace(actorTrace);
            foreach (TLCState s in states)
            {
                // Console.WriteLine(s.ToString());
                // Console.WriteLine("------------------");
                if (!this.States.Contains(s.Key()))
                {
                    newStateCount++;
                    this.ActualStates.Add(s.ToString());
                    this.States.Add(s.Key());
                }

                traceString += s.Key().ToString() + ",";
            }

            this.StateHistory.Add(this.States.Count);

            if (!this.UniqueTraces.Contains(traceString) && this.Mode == RunMode.TRACE)
            {
                newTraceCount++;
                this.UniqueTraces.Add(traceString);
            }

            if ((newStateCount > 0 && this.Mode == RunMode.STATE) || (newTraceCount > 0 && this.Mode == RunMode.TRACE))
            {
                int iters = this.Mode == RunMode.STATE ? newStateCount : newTraceCount;
                for (int i = 0; i < iters; i++)
                {
                    ExecutionTrace ex_ = this.Mutator.Mutate(actorTrace, trace, out ActorExecutionTrace at);
                    this.TraceQueue.Enqueue(new Tuple<ActorExecutionTrace, ExecutionTrace>(at, ex_));
                }
            }

            if (this.TraceQueue.Count > 0)
            {
                Tuple<ActorExecutionTrace, ExecutionTrace> traces = this.TraceQueue.Dequeue();
                // this.CurTrace = this.Mutator.Mutate(toMutate.Item1, toMutate.Item2, out ActorExecutionTrace at);
                this.CurTrace = traces.Item2;
                this.BooleanChoices.Clear();
                for (int i = 0; i < this.CurTrace.Length; i++)
                {
                    ExecutionTrace.Step step = this.CurTrace[this.StepCount];
                    if (step.Kind == ExecutionTrace.DecisionKind.NondeterministicChoice && step.IntegerChoice.HasValue)
                    {
                        this.BooleanChoices.Enqueue(step.BooleanChoice.Value);
                    }
                }
            }
            else
            {
                this.CurTrace = null;
            }

            return true;
        }

        bool IActorBasedStrategy.Finalize(LogWriter logWriter)
        {
            logWriter.LogImportant($"Total states seen: {this.States.Count}");
            logWriter.LogImportant($"Total traces seen: {this.UniqueTraces.Count}");
            this.ExportCsv();
            return true;
        }

        /// <inheritdoc/>
        internal override bool NextOperation(IEnumerable<ControlledOperation> ops, ControlledOperation current, bool isYielding, out ControlledOperation next)
        {
            if (this.CurTrace != null && this.StepCount < this.CurTrace.Length && false)
            {
                ExecutionTrace.Step step = this.CurTrace[this.StepCount];
                switch (step.Kind)
                {
                    case ExecutionTrace.DecisionKind.SchedulingChoice:
                    foreach (ControlledOperation op in ops)
                    {
                        if (op.Id == step.ScheduledOperationId)
                        {
                            next = op;
                            return true;
                        }
                    }

                    break;
                    default:
                    break;
                }
            }

            return base.NextOperation(ops,
                                            current,
                                            isYielding,
                                            out next);
        }

        internal override bool NextBoolean(ControlledOperation current, out bool next)
        {
            if (this.BooleanChoices.Count > 0 && false)
            {
                next = this.BooleanChoices.Dequeue();
                return true;
            }

            return base.NextBoolean(current, out next);
        }

        internal override bool NextInteger(ControlledOperation current, int maxValue, out int next)
        {
            if (this.CurTrace != null && this.StepCount < this.CurTrace.Length && false)
            {
                ExecutionTrace.Step step = this.CurTrace[this.StepCount];
                switch (step.Kind)
                {
                    case ExecutionTrace.DecisionKind.NondeterministicChoice:
                    if (step.IntegerChoice.HasValue)
                    {
                        next = step.IntegerChoice.Value;
                        return true;
                    }

                    break;
                }
            }

            return base.NextInteger(current, maxValue, out next);
        }

        /// <inheritdoc/>
        internal override string GetName() => "InterleavedFuzzingStrategy";

        /// <inheritdoc/>
        internal override string GetDescription() => "Fuzzing Strategy";

        internal void ExportCsv()
        {
            using (var writer = new StreamWriter($"{this.CsvPath}.csv"))
            {
                using (var csv = new CsvWriter(writer, CultureInfo.InvariantCulture))
                {
                    csv.WriteRecords(this.StateHistory);
                    System.IO.File.WriteAllLines($"{this.CsvPath}_actual.txt", this.ActualStates);
                }
            }
        }
    }
}
