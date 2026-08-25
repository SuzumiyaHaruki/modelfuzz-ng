// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading;
using CsvHelper;
using Microsoft.Coyote.Actors;
using Microsoft.Coyote.Actors.Mocks;
using Microsoft.Coyote.Logging;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Actors
{
    /// <summary>
    /// A fuzzing strategy class.
    /// </summary>
    internal class InterleavedRoundRobinStrategy : InterleavedFuzzingStrategy, IActorBasedStrategy
    {
        internal int Index;

        internal int BoolIndex;

        internal int Length;

        internal int RandomCount;

        internal int IterCount;

        internal ActorExecutionTrace CurActorTrace;

        internal List<bool> BoolChoices;

        internal int ReSeedInterval;

        /// <summary>
        /// Initializes a new instance of the <see cref="InterleavedRoundRobinStrategy"/> class.
        /// </summary>
        public InterleavedRoundRobinStrategy(Configuration configuration, IRandomValueGenerator randomValueGenerator)
            : base(configuration, randomValueGenerator)
        {
            this.CurActorTrace = null;
            this.RandomCount = 0;
            this.IterCount = 0;
            this.ReSeedInterval = 1000;
        }

        private static string GenerateTrace(ActorExecutionTrace trace)
        {
            string traceString = string.Empty;
            foreach (Step s in trace)
            {
                string eventType = s.Type;
                switch (eventType)
                {
                    case "SendEvent":
                        SendEventStep send = (SendEventStep)s;
                        if (send.Actor == null || send.Receiver == null)
                        {
                            break;
                        }

                        string sender_id = ((int)send.Actor.Value).ToString();
                        string receiver_id = ((int)send.Receiver.Value).ToString();
                        string event_ = send.Event.ToString();

                        traceString += $"{sender_id}_{receiver_id}_{event_}_";
                        break;
                    case "InvokedAction":
                        ActionInvokedStep action = (ActionInvokedStep)s;

                        string actor_id = ((int)action.Actor.Value).ToString();
                        string action_ = action.InvokedAction;

                        traceString += $"{actor_id}_{action_}_";
                        break;
                    case "ReceiveEvent":
                        ReceiveEventStep receive = (ReceiveEventStep)s;
                        if (receive.Sender == null || receive.Actor == null)
                        {
                            break;
                        }

                        sender_id = ((int)receive.Sender.Value).ToString();
                        receiver_id = ((int)receive.Actor.Value).ToString();
                        event_ = receive.Event.ToString();

                        traceString += $"{sender_id}_{receiver_id}_{event_}_";
                        break;
                }
            }

            return traceString;
        }

        bool IActorBasedStrategy.InitializeNextIteration(uint iteration, ActorExecutionTrace trace, ExecutionTrace ex)
        {
            // this.LogWriter.LogImportant($"Initializing iteration: {iteration + 1}");
            this.IterCount++;
            this.StepCount = 0;
            this.Index = 0;
            this.BoolIndex = 0;
            this.TlcClient.LogWriter = this.LogWriter;

            if (iteration == 0 || trace == null || ex == null)
            {
                this.CurTrace = null;
                this.CurActorTrace = null;
                this.BoolChoices = null;
                this.RandomCount++;
                return true;
            }

            this.LogWriter.WriteLine("Sending trace");
            List<TLCState> states = this.TlcClient.SendTrace(trace, true);
            int newStateCount = 0;
            int newTraceCount = 0;
            // string traceString = string.Empty;
            string traceString = InterleavedRoundRobinStrategy.GenerateTrace(trace);
            // Console.Write("State strings:");
            foreach (TLCState s in states)
            {
                // S.Key() is System.Int64
                // Console.WriteLine("Key and Key Type:");
                // Console.WriteLine(s.Key());
                // Console.WriteLine(s.Key().GetType());
                // Console.WriteLine("------------------");
                if (!this.States.Contains(s.Key()))
                {
                    newStateCount++;
                    this.ActualStates.Add(s.ToString());
                    this.States.Add(s.Key());
                }
            }

            this.StateHistory.Add(this.States.Count);

            if (this.Mode == 0)
            {
                this.CurTrace = null;
                this.CurActorTrace = null;
                this.BoolChoices = null;
                this.RandomCount++;
                return true;
            }

            if (!this.UniqueTraces.Contains(traceString) && this.Mode == RunMode.TRACE)
            {
                newTraceCount++;
                this.UniqueTraces.Add(traceString);
            }

            // Console.WriteLine($"New state count: {newStateCount}");
            if ((newStateCount > 0 && this.Mode == RunMode.STATE) || (newTraceCount > 0 && this.Mode == RunMode.TRACE))
            {
                int iters = this.Mode == RunMode.STATE ? newStateCount : newTraceCount;
                for (int i = 0; i < iters * 5; i++)
                {
                    ExecutionTrace ex_ = this.Mutator.Mutate(trace, ex, out ActorExecutionTrace at);
                    this.TraceQueue.Enqueue(new Tuple<ActorExecutionTrace, ExecutionTrace>(at, ex_));
                }
            }

            if (this.TraceQueue.Count > 0)
            {
                Tuple<ActorExecutionTrace, ExecutionTrace> traces = this.TraceQueue.Dequeue();
                // TODO: Allowing mapping the mutated trace to record only relevant information
                // For example, currently we only want to record non deterministic choices
                this.CurActorTrace = traces.Item1;
                this.CurTrace = traces.Item2;
                this.Length = this.CurActorTrace.Count;
            }
            else
            {
                this.CurTrace = null;
                this.CurActorTrace = null;
                this.RandomCount++;
            }

            this.BoolChoices = this.ExtractBooleanChoices();

            return true;
        }

        bool IActorBasedStrategy.Finalize(LogWriter logWriter)
        {
            logWriter.LogImportant($"Total states seen: {this.States.Count}");
            logWriter.LogImportant($"Total traces seen: {this.UniqueTraces.Count}");
            logWriter.LogImportant($"Number of random schedules: {this.RandomCount}");
            this.ExportCsv();
            // foreach (string state in this.ActualStates)
            // {
            //     Console.WriteLine(state);
            // }

            return true;
        }

        /// <inheritdoc/>
        internal override bool NextOperation(IEnumerable<ControlledOperation> ops, ControlledOperation current, bool isYielding, out ControlledOperation next)
        {
            if (this.CurActorTrace != null && this.Mode != RunMode.RANDOM && this.CurActorTrace.Count > 0)
            {
                int idx = 0;
                int j = 0;
                do
                {
                    this.Index = (this.Index >= this.CurActorTrace.Count) ? this.Index % this.CurActorTrace.Count : this.Index;
                    Step s = this.CurActorTrace[this.Index];
                    if (s.Type == "SendEvent" || s.Type == "InvokedAction" || s.Type == "ReceiveEvent")
                    {
                        ActorStep step = (ActorStep)s;
                        if (step.Actor != null)
                        {
                            idx = InterleavedRoundRobinStrategy.FindIndex(ops, step.Actor.Name);
                        }
                    }

                    if (idx != 0)
                    {
                        this.CurActorTrace.RemoveAt(this.Index);
                        next = ops.ElementAt(idx);
                        return true;
                    }

                    j++;
                    this.Index++;
                }
                while (j < this.CurActorTrace.Count);

                // Cannot make a decision, fallback to random
                return base.NextOperation(ops,
                                            current,
                                            isYielding,
                                            out next);
            }
            else
            {
                return base.NextOperation(ops,
                                            current,
                                            isYielding,
                                            out next);
            }
        }

        internal static int FindIndex(IEnumerable<ControlledOperation> ops, string actor)
        {
            for (int i = 0; i < ops.Count(); i++)
            {
                if (actor.Equals(ops.ElementAt(i).Name))
                {
                    return i;
                }
            }

            return 0;
        }

        internal override bool NextBoolean(ControlledOperation current, out bool next)
        {
            if (this.BoolChoices != null)
            {
                next = this.BoolChoices[this.BoolIndex];
                this.BoolIndex = (this.BoolIndex + 1) % this.BoolChoices.Count;
                return true;
            }

            return base.NextBoolean(current, out next);
        }

        internal List<bool> ExtractBooleanChoices()
        {
            if (this.CurTrace != null)
            {
                List<bool> l = new List<bool>();
                for (int i = 0; i < this.CurTrace.Length; i++)
                {
                    if (this.CurTrace[i].Kind == ExecutionTrace.DecisionKind.NondeterministicChoice && this.CurTrace[i].BooleanChoice.HasValue)
                    {
                        l.Add(this.CurTrace[i].BooleanChoice.Value);
                    }
                }

                return l;
            }

            return null;
        }

        internal override bool NextInteger(ControlledOperation current, int maxValue, out int next)
        {
            // TODO: Check if there is a corresponding step in the current ActorExecutionTrace to decide this.
            return base.NextInteger(current, maxValue, out next);
        }

        /// <inheritdoc/>
        internal override string GetName() => "InterleavedRoundRobinFuzzingStrategy";

        /// <inheritdoc/>
        internal override string GetDescription() => "Round Robin Fuzzing Strategy";
    }
}
