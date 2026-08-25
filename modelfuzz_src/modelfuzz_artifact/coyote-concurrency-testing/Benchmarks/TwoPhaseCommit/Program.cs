// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Coyote benchmark: TwoPhase Commit

using System.Runtime.CompilerServices;
using Microsoft.Coyote.Actors;
using Microsoft.Coyote.SystematicTesting;

namespace TwoPhaseCommit;

public static class Program
{
    [Test]
    public static void Execute(IActorRuntime runtime)
    {
        var testScenario = new TestScenario();
        testScenario.RunTest(runtime, 3, 1, 3);
    }
}
