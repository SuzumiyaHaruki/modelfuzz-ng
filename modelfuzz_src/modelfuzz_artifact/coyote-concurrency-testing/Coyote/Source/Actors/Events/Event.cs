// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System.Collections.Generic;
using System.Runtime.Serialization;

namespace Microsoft.Coyote.Actors
{
    /// <summary>
    /// Abstract class representing an event that can be send to
    /// an <see cref="Actor"/> or <see cref="StateMachine"/>.
    /// </summary>
    [DataContract]
    public abstract class Event
    {
        /// <summary>
        /// Returns a dictionary of parameters specific to this event.
        /// </summary>
#pragma warning disable CA1822
        public virtual Dictionary<string, object> GetParams()
#pragma warning restore CA1822
        {
            return new Dictionary<string, object>();
        }
    }
}
