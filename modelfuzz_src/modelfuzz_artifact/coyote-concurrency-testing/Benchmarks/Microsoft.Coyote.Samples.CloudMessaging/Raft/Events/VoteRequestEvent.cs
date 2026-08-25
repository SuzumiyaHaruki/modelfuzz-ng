// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

using System.Collections.Generic;
using System.Runtime.Serialization;
using Microsoft.Coyote.Actors;

namespace Microsoft.Coyote.Samples.CloudMessaging.Events
{
    /// <summary>
    /// Initiated by candidates during elections.
    /// </summary>
    [DataContract]
    public class VoteRequestEvent : Event
    {
        /// <summary>
        /// The candidate's term.
        /// </summary>
        [DataMember]
        public readonly int Term;

        /// <summary>
        /// The id of the candidate requesting the vote.
        /// </summary>
        [DataMember]
        public readonly string CandidateId;

        /// <summary>
        /// The index of the candidate’s last log entry.
        /// </summary>
        [DataMember]
        public readonly int LastLogIndex;

        /// <summary>
        /// The term of the candidate’s last log entry.
        /// </summary>
        [DataMember]
        public readonly int LastLogTerm;

        public VoteRequestEvent(int term, string candidateId, int lastLogIndex, int lastLogTerm)
        {
            this.Term = term;
            this.CandidateId = candidateId;
            this.LastLogIndex = lastLogIndex;
            this.LastLogTerm = lastLogTerm;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>()
            {
                { "term", this.Term },
                { "index", this.LastLogIndex },
                { "log_term", this.LastLogTerm }
            };
        }
    }
}
