// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System;
using System.Collections.Generic;
using System.Linq;
using Microsoft.Coyote.Runtime;

namespace Microsoft.Coyote.Testing.Interleaving
{
    /// <summary>
    /// A simple (but effective) randomized exploration strategy.
    /// </summary>
    internal class RandomChoiceStrategy : InterleavingStrategy
    {
        /// <summary>
        /// Initializes a new instance of the <see cref="RandomChoiceStrategy"/> class.
        /// </summary>
        internal RandomChoiceStrategy(Configuration configuration, bool isFair = true)
            : base(configuration, isFair)
        {
        }

        /// <inheritdoc/>
        internal override bool NextOperation(IEnumerable<ControlledOperation> ops, ControlledOperation current,
            bool isYielding, out ControlledOperation next)
        {
            Console.WriteLine("List of next operations: ");

            for (int j = 0; j < ops.Count(); j++)
            {
                Console.WriteLine($"{j}. {ops.ElementAt(j).Name}");
            }

            Console.Write("Please select the next operation: ");
            int idx = Convert.ToInt32(Console.ReadLine());
            int i = 0;
            while (idx >= ops.Count())
            {
                Console.WriteLine("Invalid input, try again. List of next operations: ");
                i = 0;
                foreach (ControlledOperation operation in ops)
                {
                    Console.WriteLine($"{i}. {operation.ToString()}");
                    i++;
                }

                Console.Write("Please select the next operation: ");
                idx = Convert.ToInt32(Console.ReadLine());
            }

            // int idx = this.RandomValueGenerator.Next(ops.Count());
            next = ops.ElementAt(idx);
            return true;
        }

        /// <inheritdoc/>
        internal override bool NextBoolean(ControlledOperation current, out bool next)
        {
            Console.Write("Enter boolean for the next operation (0 for True, 1 for False): ");
            int i = Convert.ToInt32(Console.ReadLine());
            while (i != 0 || i != 1)
            {
                Console.Write("Invalid input, try again. Enter boolean for the next operation (0 for True, 1 for False): ");
                i = Convert.ToInt32(Console.ReadLine());
            }

            next = i is 0 ? true : false;
            return true;
        }

        /// <inheritdoc/>
        internal override bool NextInteger(ControlledOperation current, int maxValue, out int next)
        {
            Console.Write($"Enter integer in the range [0, {maxValue}) for the next operation: ");
            next = Convert.ToInt32(Console.ReadLine());
            while (next < 0 || next >= maxValue)
            {
                Console.Write($"Invalid input, try again. Enter integer in the range [0, {maxValue}) for the next operation: ");
                next = Convert.ToInt32(Console.ReadLine());
            }

            // next = this.RandomValueGenerator.Next(maxValue);
            return true;
        }

        /// <inheritdoc/>
        internal override string GetName() => ExplorationStrategy.RandomChoice.GetName();

        /// <inheritdoc/>
        internal override string GetDescription() => $"{this.GetName()}[seed:{this.RandomValueGenerator.Seed}]";
    }
}
