// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Coyote benchmark: TwoPhase Commit

using System.Collections.Generic;
using System.Runtime.Serialization;
using Microsoft.Coyote.Actors;

namespace TwoPhaseCommit
{
    /// <summary>
    /// Notifies a server that it has joined the Raft
    /// service and can start executing.
    /// </summary>
    [DataContract]
    public class RegisterServerEvent : Event
    {
        /// <summary>
        /// The server id that is being registered.
        /// </summary>
        public ActorId ServerId;
    }

    /// <summary>
    /// Used to issue a client request.
    /// </summary>
    [DataContract]
    public class ClientRequestEvent : Event
    {
        [DataMember]
        public readonly ActorId Sender;

        [DataMember]
        public readonly string Command;

        [DataMember]
        public readonly int RequestId;

        public ClientRequestEvent(ActorId sender, string command, int requestId)
        {
            this.Sender = sender;
            this.Command = command;
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>() { { "request_id", this.RequestId } };
        }
    }

    /// <summary>
    /// Used to issue a client response.
    /// </summary>
    [DataContract]
    public class ClientResponseEvent : Event
    {
        [DataMember]
        public readonly bool IsSuccessful;

        [DataMember]
        public readonly int RequestId;

        public ClientResponseEvent(bool isSuccessful, int requestId)
        {
            this.IsSuccessful = isSuccessful;
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>() { { "request_id", this.RequestId } };
        }
    }

    internal class GlobalAbortEvent : Event
    {
        [DataMember]
        public readonly int RequestId;

        public GlobalAbortEvent(int requestId)
        {
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>() { { "request_id", this.RequestId } };
        }
    }

    internal class GlobalCommitEvent : Event
    {
        [DataMember]
        public readonly int RequestId;

        public GlobalCommitEvent(int requestId)
        {
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>() { { "request_id", this.RequestId } };
        }
    }

    internal class GlobalResetEvent : Event
    {
        [DataMember]
        public readonly int RequestId;

        public GlobalResetEvent(int requestId)
        {
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>() { { "request_id", this.RequestId } };
        }
    }

    internal class AbortEvent : Event
    {
        [DataMember]
        public readonly ActorId Sender;

        /// <summary>
        /// The reason for voting abort.
        /// </summary>
        [DataMember]
        public readonly string Reason;

        [DataMember]
        public readonly int RequestId;

        public AbortEvent(ActorId sender, string reason, int requestId)
        {
            this.Sender = sender;
            this.Reason = reason;
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>() { { "request_id", this.RequestId } };
        }
    }

    internal class PreparedEvent : Event
    {
        /// <summary>
        /// The coordinator/sender of the event
        /// </summary>
        [DataMember]
        public readonly ActorId Sender;

        [DataMember]
        public readonly int RequestId;

        public PreparedEvent(ActorId sender, int requestId)
        {
            this.Sender = sender;
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>() { { "request_id", this.RequestId } };
        }
    }

    internal class RequestEvent : Event
    {
        /// <summary>
        /// The coordinator/sender of the event
        /// </summary>
        [DataMember]
        public readonly ActorId Sender;

        [DataMember]
        public readonly int RequestId;

        public RequestEvent(ActorId sender, int requestId)
        {
            this.Sender = sender;
            this.RequestId = requestId;
        }

        public override Dictionary<string, object> GetParams()
        {
            return new Dictionary<string, object>() { { "request_id", this.RequestId } };
        }
    }
}
