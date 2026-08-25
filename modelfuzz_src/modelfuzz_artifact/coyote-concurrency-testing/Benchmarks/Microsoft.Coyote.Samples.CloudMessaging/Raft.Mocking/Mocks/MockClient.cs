// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Text;
using System.Threading.Tasks;
using Microsoft.Coyote.Actors;
using Microsoft.Coyote.Actors.Timers;
using Microsoft.Coyote.Samples.CloudMessaging.Events;

namespace Microsoft.Coyote.Samples.CloudMessaging.Mocks
{
    /// <summary>
    /// Mock implementation of a client that sends a specified number of requests to
    /// the Raft cluster.
    /// </summary>
    [OnEventDoAction(typeof(ClientResponseEvent), nameof(HandleResponse))]
    [OnEventDoAction(typeof(TimerElapsedEvent), nameof(HandleTimeout))]
    public class MockClient : Actor
    {
        public class SetupEvent : Event
        {
            internal readonly ActorId Cluster;
            internal readonly int NumRequests;
            internal readonly TimeSpan RetryTimeout;
            public TaskCompletionSource<bool> Finished;

            public SetupEvent(ActorId cluster, int numRequests, TimeSpan retryTimeout)
            {
                this.Cluster = cluster;
                this.NumRequests = numRequests;
                this.RetryTimeout = retryTimeout;
                this.Finished = new TaskCompletionSource<bool>();
            }
        }

        private SetupEvent ClientInfo;
        private int NumResponses;

        private string NextCommand => $"request-{this.NumResponses}";

        protected override Task OnInitializeAsync(Event initialEvent)
        {
            var setup = initialEvent as SetupEvent;
            this.ClientInfo = setup;
            this.NumResponses = 0;

            // Start by sending the first request.
            this.SendNextRequest();

            // Create a periodic timer to retry sending requests, if needed.
            // The chosen time does not matter, as the client will run under
            // test mode, and thus the time is controlled by the runtime.
            this.StartPeriodicTimer(TimeSpan.FromSeconds(10), TimeSpan.FromSeconds(20));
            return Task.CompletedTask;
        }

        private void SendNextRequest()
        {
            Console.WriteLine($"Client command is follows: {Convert.ToBase64String(Encoding.UTF8.GetBytes(this.NextCommand))}");
            this.SendEvent(this.ClientInfo.Cluster, new ClientRequestEvent(Convert.ToBase64String(Encoding.UTF8.GetBytes(this.NextCommand)), this.NumResponses));

            this.Logger.WriteLine($"<Client> sent {this.NextCommand}.");
        }

        private void HandleResponse(Event e)
        {
            var response = e as ClientResponseEvent;
            if (response.Command == Convert.ToBase64String(Encoding.UTF8.GetBytes(this.NextCommand)))
            {
                this.Logger.WriteLine($"<Client> received response for {response.Command} from  {response.Server}.");
                this.NumResponses++;

                if (this.NumResponses == this.ClientInfo.NumRequests)
                {
                    // Halt the client, as all responses have been received.
                    this.RaiseHaltEvent();
                    this.ClientInfo.Finished.SetResult(true);
                }
                else
                {
                    this.SendNextRequest();
                }
            }
        }

        /// <summary>
        /// Retry to send the request.
        /// </summary>
        private void HandleTimeout() => this.SendNextRequest();
    }
}
