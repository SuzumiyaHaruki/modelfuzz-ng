// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;
using System.IO;
using System.Net.Http;
using System.Net.Sockets;
using System.Text.Json;
using System.Threading.Tasks;
using Microsoft.Coyote.Logging;

namespace Microsoft.Coyote.Actors
{
    internal class TLCState
    {
        private string StateString { get; }

        private long StateKey { get; }
        public TLCState(string state, long key)
        {
            this.StateString = state;
            this.StateKey = key;
        }

        public override string ToString()
        {
            return this.StateString;
        }

        public long Key()
        {
            return this.StateKey;
        }
    }

    internal sealed class TLCEvent
    {
        public string Name { get; set; }
        public Dictionary<string, object> Params { get; set; }
        public bool Reset { get; set; }

        public static TLCEvent NewEvent(string t, Dictionary<string, object> parameters)
        {
            TLCEvent e = new TLCEvent();
            e.Name = t;
            e.Params = parameters;
            e.Reset = false;
            return e;
        }

        public static TLCEvent NewReset()
        {
            TLCEvent e = new TLCEvent();
            e.Reset = true;
            return e;
        }
    }

    internal sealed class TLCServerResponse
    {
        public IList<string> States { get; set; }

        public IList<long> Keys { get; set; }
    }

    internal sealed class TLCClient
    {
        internal string Server { get; set; }
        internal int Port { get; set; }

        internal HttpClient HttpClient { get; }

        internal LogWriter LogWriter { get; set; }

        internal int IndexOffset { get; set; }

        internal List<TLCEvent> Trace;

        public TLCClient(int indexOffset = 1, string server = "127.0.0.1", int port = 2023)
        {
            this.Server = server;
            this.Port = port;
            this.HttpClient = new HttpClient();
            this.IndexOffset = indexOffset;
            this.Trace = new List<TLCEvent>();
        }

        private static byte[] ReadAll(NetworkStream stream)
        {
            MemoryStream ms = new MemoryStream();
            byte[] data = new byte[1024];
            int numBytesRead = stream.Read(data, 0, data.Length);
            ms.Write(data, 0, numBytesRead);
            while (numBytesRead > 0 && numBytesRead == data.Length)
            {
                numBytesRead = stream.Read(data, 0, data.Length);
                ms.Write(data, 0, numBytesRead);
            }

            return ms.ToArray();
        }

        private string SendRequest(string json)
        {
            HttpRequestMessage request = new HttpRequestMessage(HttpMethod.Post, "http://" + this.Server + ":" + this.Port.ToString() + "/execute");
            request.Content = new ByteArrayContent(System.Text.Encoding.UTF8.GetBytes(json));
            HttpResponseMessage response = this.HttpClient.Send(request);
            // Console.WriteLine("Response: " + response);

            Task<string> readResponseTask = response.Content.ReadAsStringAsync();
            readResponseTask.Wait();
            // Console.WriteLine("Response: " + response);
            return readResponseTask.Result;
        }

        internal List<TLCState> SendTrace(ActorExecutionTrace t, bool reset = false)
        {
            List<TLCEvent> jsonList = TLCClient.ToJson(t, this.IndexOffset, reset);
            this.Trace.AddRange(jsonList);
            string json = JsonSerializer.Serialize(jsonList);
            // Console.WriteLine("Sending trace to TLC: " + json);

            try
            {
                // Sending data
                // Console.WriteLine("Sending...");
                string responseString = this.SendRequest(json);
                // Console.WriteLine("ResponseString: " + responseString);
                responseString = responseString.Replace("\n", string.Empty);
                TLCServerResponse response = JsonSerializer.Deserialize<TLCServerResponse>(responseString, new JsonSerializerOptions { PropertyNameCaseInsensitive = true } );

                List<TLCState> result = new List<TLCState>();
                for (int i = 0; i < response.States.Count; i++)
                {
                    result.Add(new TLCState(response.States[i], response.Keys[i]));
                }

                return result;
            }
            catch
            {
                // this.LogWriter.LogError("Error writing data to TLC socket: " + ex.ToString());
                string traceJson = JsonSerializer.Serialize(this.Trace);
                // Console.WriteLine(traceJson);
            }

            return new List<TLCState>();
        }

        internal static List<TLCEvent> ToJson(ActorExecutionTrace t, int indexOffset, bool reset)
        {
            List<TLCEvent> jsonList = new List<TLCEvent>();
            foreach (Step s in t)
            {
                string eventType = s.Type;
                Dictionary<string, object> parameters = null;
                switch (s.Type)
                {
                    case "SendEvent":
                        SendEventStep send = (SendEventStep)s;
                        if (send.Actor == null || send.Receiver == null)
                        {
                            break;
                        }

                        parameters = new Dictionary<string, object>()
                        {
                            { "sender", send.Actor.ToString() },
                            { "sender_id", ((int)send.Actor.Value - indexOffset).ToString() },
                            { "receiver", send.Receiver.ToString() },
                            { "receiver_id", ((int)send.Receiver.Value - indexOffset).ToString() },
                            { "event", send.Event.ToString() }
                        };
                        foreach (KeyValuePair<string, object> eventParams in send.Event.GetParams())
                        {
                            parameters.Add(eventParams.Key, eventParams.Value);
                        }

                        break;
                    case "InvokedAction":
                        ActionInvokedStep action = (ActionInvokedStep)s;
                        parameters = new Dictionary<string, object>
                        {
                            { "actor", action.Actor.ToString() },
                            { "actor_id", ((int)action.Actor.Value - indexOffset).ToString() },
                            { "action", action.InvokedAction },
                        };
                        break;
                    case "NonDeterministicBooleanChoiceStep":
                        NonDeterministicBooleanChoiceStep choiceStep = (NonDeterministicBooleanChoiceStep)s;
                        parameters = new Dictionary<string, object>
                        {
                            { "choice", choiceStep.Choice }
                        };
                        break;
                    case "ReceiveEvent":
                        ReceiveEventStep receive = (ReceiveEventStep)s;
                        if (receive.Sender == null || receive.Actor == null)
                        {
                            break;
                        }

                        parameters = new Dictionary<string, object>
                        {
                            { "sender", receive.Sender.ToString() },
                            { "sender_id", ((int)receive.Sender.Value - indexOffset).ToString() },
                            { "receiver", receive.Actor.ToString() },
                            { "receiver_id", ((int)receive.Actor.Value - indexOffset).ToString() },
                            { "event", receive.Event.ToString() }
                        };
                        foreach (KeyValuePair<string, object> eventParams in receive.Event.GetParams())
                        {
                            parameters.Add(eventParams.Key, eventParams.Value);
                        }

                        break;
                    case "StateTransition":
                        StateTransitionStep transitionStep = (StateTransitionStep)s;
                        parameters = new Dictionary<string, object>
                        {
                            { "actor", transitionStep.Actor.ToString() },
                            { "actor_id", ((int)transitionStep.Actor.Value - indexOffset).ToString() },
                            { "from", transitionStep.FromState },
                            { "to", transitionStep.ToState }
                        };
                        break;
                }

                if (parameters != null)
                {
                    jsonList.Add(TLCEvent.NewEvent(eventType, parameters));
                }
            }

            if (reset)
            {
                jsonList.Add(TLCEvent.NewReset());
            }

            return jsonList;
        }
    }
}
