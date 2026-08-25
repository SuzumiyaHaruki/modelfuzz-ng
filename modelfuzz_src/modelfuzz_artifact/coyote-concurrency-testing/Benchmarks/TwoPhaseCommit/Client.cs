// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Coyote benchmark: TwoPhase Commit

using System;
using System.Threading.Tasks;
using Microsoft.Coyote.Actors;
using Microsoft.Coyote.Actors.Timers;

namespace TwoPhaseCommit;

/// <summary>
/// Mock implementation of a client that sends a specified number of requests to
/// the TwoPhaseCommit Coordinator.
/// Note: The current implementation of TwoPhaseCommit protocol
/// supports processing only a single client request
/// </summary>
[OnEventDoAction(typeof(ClientResponseEvent), nameof(HandleResponse))]
[OnEventDoAction(typeof(TimerElapsedEvent), nameof(HandleTimeout))]
public class Client : Actor
{
    public class SetupEvent : Event
    {
        internal readonly ActorId Coordinator;
        internal readonly int NumRequests;
        internal readonly int MaxNumRequests;
        internal readonly TimeSpan RetryTimeout;
        public TaskCompletionSource<bool> Finished;

        public SetupEvent(ActorId coordinator, int numRequests, int maxNumRequests, TimeSpan retryTimeout)
        {
            this.Coordinator = coordinator;
            this.NumRequests = numRequests;
            this.MaxNumRequests = maxNumRequests;
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
        // this.StartPeriodicTimer(TimeSpan.FromSeconds(1), TimeSpan.FromSeconds(1));
        return Task.CompletedTask;
    }

    private void SendNextRequest()
    {
        this.SendEvent(this.ClientInfo.Coordinator, new ClientRequestEvent(this.Id, this.NextCommand, this.NumResponses + 1));

        this.Logger.WriteLine($"<Client> sent {this.NextCommand}.");
    }

    private void HandleResponse(Event e)
    {
        var response = e as ClientResponseEvent;

        if (response != null)
        {
            this.Logger.WriteLine($"<Client> transaction request isSuccessful: {response.IsSuccessful}");
            this.NumResponses++;
            if (!response.IsSuccessful)
            {
                if (this.NumResponses >= this.ClientInfo.NumRequests)
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
            else
            {
                if (this.NumResponses < this.ClientInfo.MaxNumRequests)
                {
                    this.SendNextRequest();
                }
                else
                {
                    // Halt the client, as all responses have been received.
                    this.RaiseHaltEvent();
                    this.ClientInfo.Finished.SetResult(true);
                }
            }
        }
    }

    /// <summary>
    /// Retry to send the request.
    /// </summary>
    private void HandleTimeout()
    {
        this.Logger.WriteLine("Client handling timeout");
        this.SendNextRequest();
    }
}
