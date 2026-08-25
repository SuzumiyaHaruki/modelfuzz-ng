// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Coyote benchmark: TwoPhase Commit

using System;
using Microsoft.Coyote.Actors;

namespace TwoPhaseCommit;

public class TestScenario
{
    public void RunTest(IActorRuntime runtime, int numRMs, int numRequests, int maxNumRequests)
    {
        // Register a safety monitor for checking the specification that
        // only one leader can be elected at any given term.
        // runtime.RegisterMonitor<SafetyMonitor>();

        // Create the actor for the coordinator
        // var cluster = this.CreateCoordinator(runtime);
        var cluster = this.CreateCoordinator(runtime);

        for (int serverId = 0; serverId < numRMs; serverId++)
        {
            // var actorId = runtime.CreateActorIdFromName(typeof(ResourceManager), $"RM-{serverId}");
            ActorId actorId = runtime.CreateActor(typeof(ResourceManager));

            // pass the remote server id's to the ClusterManager.
            runtime.SendEvent(cluster, new RegisterServerEvent() { ServerId = actorId });
        }

        // Create the actor id for a client that will be sending requests to the Raft service.
        // var client = runtime.CreateActorIdFromName(typeof(Client), "Client");

        // Create the client actor instance, so the runtime starts executing it.
        runtime.CreateActor(typeof(Client), new Client.SetupEvent(cluster, numRequests, maxNumRequests, TimeSpan.FromSeconds(1)));
    }

    protected virtual ActorId CreateCoordinator(IActorRuntime runtime) =>
        runtime.CreateActor(typeof(Coordinator));
}
