// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;

namespace Microsoft.Coyote.Actors
{
    internal static class TraceGenerator
    {
        internal static string GetTrace(ActorExecutionTrace trace)
        {
            List<List<string>> actorActions = new List<List<string>>();
            int index = -1;

            foreach (Step s in trace)
            {
                if (s.Type == "InvokedAction")
                {
                    ActionInvokedStep ais = (ActionInvokedStep)s;

                    if (ais.Actor == null)
                    {
                        continue;
                    }

                    index = (int)ais.Actor.Value;

                    if (actorActions.Count < index + 1)
                    {
                        do
                        {
                            actorActions.Add(new List<string>());
                        }
                        while (actorActions.Count < index + 1);
                    }

                    actorActions[index].Add($"A{index}-{ais.InvokedAction}");
                }
                else if (s.Type == "SendEvent")
                {
                    SendEventStep ses = (SendEventStep)s;

                    if (ses.Actor == null)
                    {
                        continue;
                    }

                    index = (int)ses.Actor.Value;

                    if (actorActions.Count < index + 1)
                    {
                        do
                        {
                            actorActions.Add(new List<string>());
                        }
                        while (actorActions.Count < index + 1);
                    }

                    actorActions[index].Add($"A{index}-{ses.Event.ToString()}");
                }
            }

            string traceString = string.Empty;

            foreach (List<string> l in actorActions)
            {
                foreach (string str in l)
                {
                    traceString += str + ",";
                }
            }

            return traceString;
        }
    }
}
