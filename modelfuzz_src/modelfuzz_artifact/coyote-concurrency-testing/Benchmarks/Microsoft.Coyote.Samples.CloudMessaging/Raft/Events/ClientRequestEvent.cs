// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System.Collections.Generic;
using System.Runtime.Serialization;
using Microsoft.Coyote.Actors;

namespace Microsoft.Coyote.Samples.CloudMessaging.Events
{
    /// <summary>
    /// Used to issue a client request.
    /// </summary>
    [DataContract]
    public class ClientRequestEvent : Event
    {
        [DataMember]
        public readonly string Command;

        public int RequestId;

        public int LeaderId;

        public ClientRequestEvent(string command, int requestId)
        {
            this.Command = command;
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>()
            {
                { "request", this.RequestId }
            };
        }
    }
}
