// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Coyote benchmark: TwoPhase Commit

using Microsoft.Coyote.Actors;

namespace TwoPhaseCommit;

public class ResourceManager : StateMachine
{
    [Start]
    [OnEventDoAction(typeof(RequestEvent), nameof(HandleRequest))]
    [OnEventDoAction(typeof(GlobalAbortEvent), nameof(HandleGlobalAbort))] // Fix by adding back!
    private class Working : State { }

    // [OnEntry(nameof(BecomePrepared))]
    [OnEventDoAction(typeof(GlobalAbortEvent), nameof(HandleGlobalAbort))]
    [OnEventDoAction(typeof(GlobalCommitEvent), nameof(HandleGlobalCommit))]
    private class Prepared : State { }

    // [OnEntry(nameof(BecomeAborted))]
    [OnEventDoAction(typeof(GlobalAbortEvent), nameof(HandleGlobalAbort))]
    private class Aborted : State { }

    // [OnEntry(nameof(BecomeCommitted))]
    [OnEventDoAction(typeof(GlobalResetEvent), nameof(HandleGlobalReset))]
    private class Committed : State { }

    /// <summary>
    /// Handle the transaction request sent by the Coordinator
    /// </summary>
    private void HandleRequest(Event e)
    {
        this.Logger.WriteLine("Handling vote request: " + this.Id);

        // RM prepares or aborts nondeterministically
        var random = Microsoft.Coyote.Random.Generator.Create();

        // Get a random boolean that can be controlled during testing.
        bool commit = random.NextBoolean();

        if (commit)
        {
            this.SendEvent(((RequestEvent)e).Sender, new PreparedEvent(this.Id, ((RequestEvent)e).RequestId));
            this.RaiseGotoStateEvent<Prepared>();
        }
        else
        {
            this.SendEvent(((RequestEvent)e).Sender, new AbortEvent(this.Id, "Aborted", ((RequestEvent)e).RequestId));
            this.RaiseGotoStateEvent<Aborted>();
        }
    }

    /// <summary>
    /// Handle the received <see cref="GlobalAbort"/> by voting based
    /// on the current role of the Raft server.
    /// </summary>
    private void HandleGlobalAbort()
    {
        this.RaiseGotoStateEvent<Aborted>();
    }

    /// <summary>
    /// Handle the received <see cref="GlobalCommit"/> by voting based
    /// on the current role of the Raft server.
    /// </summary>
    private void HandleGlobalCommit()
    {
        this.RaiseGotoStateEvent<Committed>();
    }

    /// <summary>
    /// Handle the received <see cref="GlobalReset"/> by voting based
    /// on the current role of the Raft server.
    /// </summary>
    private void HandleGlobalReset()
    {
        this.RaiseGotoStateEvent<Working>();
    }
}
