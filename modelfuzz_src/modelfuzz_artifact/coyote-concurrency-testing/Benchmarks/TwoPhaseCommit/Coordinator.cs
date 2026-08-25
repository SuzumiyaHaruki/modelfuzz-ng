// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Coyote benchmark: TwoPhase Commit

using System.Collections.Generic;
using System.Threading.Tasks;
using Microsoft.Coyote.Actors;

namespace TwoPhaseCommit;

public class Coordinator : StateMachine
{
    /// <summary>
    /// The id of the Client
    /// </summary>
    private ActorId ClientId;

    /// <summary>
    /// The set of RMs the Coordinator coordinates
    /// </summary>
    private HashSet<ActorId> RMs = new HashSet<ActorId>();

    /// <summary>
    /// The set of RM ids from which the TM has received \Prepared" messages.
    /// </summary>
    private HashSet<ActorId> PreparedRMs;

    /// <summary>
    /// The number of RM ids from which the TM has received a response.
    /// </summary>
    private int NumResponses;

    [Start]
    [OnEntry(nameof(ResetTxState))]
    [OnEventDoAction(typeof(RegisterServerEvent), nameof(RegisterServer))]
    [OnEventDoAction(typeof(ClientRequestEvent), nameof(HandleClientRequest))]
    private class Init : State { }

    [OnEventDoAction(typeof(AbortEvent), nameof(HandleAbortEvent))]
    [OnEventDoAction(typeof(PreparedEvent), nameof(HandlePreparedEvent))]
    [DeferEvents(typeof(ClientRequestEvent))]
    private class InProgress : State { }

    /// <summary>
    /// Handle the received <see cref="RequestEvent"/> by voting based
    /// on the current role of the Raft server.
    /// </summary>
    private void ResetTxState ()
    {
        this.PreparedRMs = new HashSet<ActorId>();
        this.NumResponses = 0;
    }

    public virtual async Task RegisterServer(Event e)
    {
        var reg = e as RegisterServerEvent;
        this.RMs.Add(reg.ServerId);
        await Task.CompletedTask;
        this.Logger.WriteLine("Registered server: " + reg.ServerId);
    }

    /// <summary>
    /// Handle the received client request
    /// </summary>
    private void HandleClientRequest (Event e)
    {
        this.Logger.WriteLine("Received and started processing request");

        if (((ClientRequestEvent)e).RequestId > 1)
        {
            GlobalResetEvent reset = new GlobalResetEvent(((ClientRequestEvent)e).RequestId);

            foreach (ActorId rm in this.RMs)
            {
                this.SendEvent(rm, reset);
            }
        }

        this.ClientId = ((ClientRequestEvent)e).Sender;

        RequestEvent requestEvent = new RequestEvent(this.Id, ((ClientRequestEvent)e).RequestId);

        foreach (ActorId rm in this.RMs)
        {
            this.SendEvent(rm, requestEvent);
        }

        this.RaiseGotoStateEvent<InProgress>();
    }

    /// <summary>
    /// Handle the received <see cref="AbortEvent"/>
    /// </summary>
    private void HandleAbortEvent(Event e)
    {
        this.NumResponses++;
        GlobalAbortEvent abort = new GlobalAbortEvent(((AbortEvent)e).RequestId);
        foreach (ActorId rm in this.RMs)
        {
            this.SendEvent(rm, abort);
        }

        ClientResponseEvent resultEvent = new ClientResponseEvent(false, ((AbortEvent)e).RequestId);
        this.SendEvent(this.ClientId, resultEvent);

        if (this.NumResponses == this.RMs.Count)
        {
            this.RaiseGotoStateEvent<Init>();
        }
    }

    /// <summary>
    /// Handle the received <see cref="PreparedEvent"/>
    /// </summary>
    private void HandlePreparedEvent(Event e)
    {
        this.NumResponses++;
        this.PreparedRMs.Add(((PreparedEvent)e).Sender);
        if (this.PreparedRMs.Count == this.RMs.Count)
        {
            this.Logger.WriteLine("Collected all votes, committing.");

            GlobalCommitEvent commit = new GlobalCommitEvent(((PreparedEvent)e).RequestId);
            foreach (ActorId rm in this.RMs)
            {
                this.SendEvent(rm, commit);
            }

            ClientResponseEvent resultEvent = new ClientResponseEvent(true, ((PreparedEvent)e).RequestId);
            this.SendEvent(this.ClientId, resultEvent);

            this.RaiseGotoStateEvent<Init>();
        }
    }
}
