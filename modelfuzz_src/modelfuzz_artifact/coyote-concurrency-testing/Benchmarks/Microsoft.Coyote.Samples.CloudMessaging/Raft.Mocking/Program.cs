// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using Microsoft.Coyote.Actors;
using Microsoft.Coyote.SystematicTesting;

namespace Microsoft.Coyote.Samples.CloudMessaging.Raft.Mocking
{
    public static class Program
    {
        [Test]
        public static void Execute(IActorRuntime runtime)
        {
            var testScenario = new RaftTestScenario();
            testScenario.RunTest(runtime, 3, 1);
        }
    }
}
