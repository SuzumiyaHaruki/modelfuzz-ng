// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System.Collections.Generic;
using System.Runtime.Serialization;
using Microsoft.Coyote.Actors;

namespace Microsoft.Coyote.Samples.CloudMessaging.Events
{
    /// <summary>
    /// Response to an append entries request.
    /// </summary>
    [DataContract]
    public class AppendLogEntriesResponseEvent : Event
    {
        /// <summary>
        /// The id of the server we are sending this message to.
        /// </summary>
        [DataMember]
        public readonly string To;

        /// <summary>
        /// The current term for the leader to update itself.
        /// </summary>
        [DataMember]
        public readonly int Term;

        /// <summary>
        /// True if the follower contained entry matching PrevLogIndex and PrevLogTerm.
        /// </summary>
        [DataMember]
        public readonly bool Success;

        /// <summary>
        /// The server id so leader can update its state.
        /// </summary>
        [DataMember]
        public readonly string SenderId;

        /// <summary>
        /// The commit index.
        /// </summary>
        [DataMember]
        public readonly int MatchIndex;

        /// <summary>
        /// The client request command, if any.
        /// </summary>
        [DataMember]
        public readonly string Command;

        public AppendLogEntriesResponseEvent(string to, string senderId, int term, bool success, int mIndex, string command)
        {
            this.To = to;
            this.Term = term;
            this.Success = success;
            this.SenderId = senderId;
            this.Command = command;
            this.MatchIndex = mIndex;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>()
            {
                { "term", this.Term },
                { "index", this.MatchIndex },
                { "reject", !this.Success }
            };
        }
    }
}
