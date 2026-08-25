from http import HTTPStatus
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from typing import Any
from urllib.parse import urlsplit, parse_qs
from threading import Lock, Thread
from base64 import b64encode
from socket import SHUT_RDWR
import requests
import json
import logging

class Request:
    def __init__(self, method, path, headers,  content: str, query=None) -> None:
        self.path = path
        self.headers = headers
        self.query = query
        self.content = content
        self.method = method

class Response:
    def __init__(self, status_code: int, content) -> None:
        self.status_code = status_code
        self.headers = {}
        self.content = content

    def set_header(self, key, value):
        self.headers[key] = value

    def set_content_json(self):
        self.headers["Content-Type"] = "application/json"

    @staticmethod
    def json(status_code: int, content):
        r = Response(status_code, content)
        r.set_content_json()
        return r

class Router:
    def __init__(self) -> None:
        self.handlers = {}
    
    def add_route(self, path, handler):
        self.handlers[path] = handler

    def handle(self, request):
        if request.path in self.handlers:
            response = self.handlers[request.path](request)
            return response
        return Response(HTTPStatus.NOT_FOUND, "Path does not exist")

class _ServerHandler(BaseHTTPRequestHandler):
    def __init__(self, *args, handler = None, **kwargs) -> None:
        self.handler = handler
        super().__init__(*args, **kwargs)

    def respond(self, response: Response):
        try:
            if response is None:
                self.send_error(HTTPStatus.INTERNAL_SERVER_ERROR, "Failed to generate a response")

            if response.status_code >= HTTPStatus.BAD_REQUEST:
                for key in response.headers:
                    self.send_header(key, response.headers[key])
                self.send_error(response.status_code, response.content)
            else:
                self.send_response(response.status_code)
                for key in response.headers:
                    self.send_header(key, response.headers[key])

                content = None
                if response.content is not None and len(response.content) > 0:
                    content = response.content.encode("UTF-8", "replace")
                
                if content is None:
                    self.send_header("Content-Length", str(0))
                    self.end_headers()
                else:
                    self.send_header("Content-Length", str(len(content)))
                    self.end_headers()
                    self.wfile.write(content)

                return
        except Exception as e:
            return
    
    def log_message(self, format: str, *args: Any) -> None:
       return

    def do_GET(self):
        (_, _, path, query, _) = urlsplit(self.path)
        parsed_qs = parse_qs(query)
        request = Request("GET", path, self.headers, "", parsed_qs)
        if self.handler is not None:
            response = self.handler.handle(request)
            self.respond(response)
        else:
            self.send_error(HTTPStatus.NOT_FOUND, "Request not handled")

    def do_POST(self):
        (_, _, path, query, _) = urlsplit(self.path)
        parsed_qs = parse_qs(query)
        length = int(self.headers.get('content-length')) # type: ignore
        content = ""
        if length > 0:
            content = self.rfile.read(length).decode("utf-8", "replace") 
        
        request = Request("POST", path, self.headers, content, parsed_qs)
        if self.handler is not None:
            response = self.handler.handle(request)
            self.respond(response)
        else:
            self.send_error(HTTPStatus.NOT_FOUND, "Request not handled")

    
class Server(ThreadingHTTPServer):
    def __init__(self, 
                 server_address, 
                 handler: Router,) -> None:
        self.handler = handler
        super().__init__(server_address, _ServerHandler, False)

    def start(self):
        try:
            self.server_bind()
            self.server_activate()
        except:
            self.server_close()
            raise
    
    def shutdown(self):
        super().shutdown()
        self.socket.shutdown(SHUT_RDWR)
        self.server_close()

    def finish_request(self, request, client_address) -> None:
        if self.RequestHandlerClass == _ServerHandler:
            return self.RequestHandlerClass(request, client_address, self, handler=self.handler) # type: ignore
        return super().finish_request(request, client_address)
    

class Message:
    def __init__(self, fr, to, type, msg, id=None) -> None:
        self.fr = fr
        self.to = to
        self.type = type
        self.msg = msg
        self.id = id
        self.parsed_message = json.loads(msg)

    @staticmethod
    def from_str(s: str): # type: ignore
        m = json.loads(s)
        if "from" not in m or "to" not in m or "type" not in m or "data" not in m:
            return None
        return Message(m["from"], m["to"], m["type"], m["data"], m["id"] if "id" in m else None)

    def to_obj(self):
        return {"from": self.fr, "to": self.to, "type": self.type, "data": self.msg, "id": self.id}


class Network:
    def __init__(self, addr) -> None:
        self.addr = addr
        self.lock = Lock()
        self.mailboxes = {}
        self.replicas = {}
        self.event_trace = []
        self.request_ctr = 1
        self.request_map = {}
        self.logger = logging.getLogger("fuzzer-network {}".format(self.addr))

        router = Router()
        router.add_route("/replica", self._handle_replica)
        router.add_route("/message", self._handle_message)
        router.add_route("/event", self._handle_event)

        self.server = Server(addr, router)
        self.server_thread = None

    def run(self):
        self.server.start()
        if self.server_thread is None:
            self.server_thread = Thread(target=self.server.serve_forever)
        self.server_thread.start()
    
    def shutdown(self):
        self.server.shutdown()
        if self.server_thread is not None:
            self.server_thread.join()
            self.server_thread = None
    
    def _handle_replica(self, request: Request) -> Response:
        self.logger.debug("Received replica: {}".format(request.content))
        replica = json.loads(request.content)
        if "id" in replica:
            with self.lock:
                replica_id = int(replica["id"])
                self.replicas[replica_id] = replica

        return Response.json(HTTPStatus.OK, json.dumps({"message": "Ok"}))
    
    def _get_request_number(self, data):
        if data not in self.request_map:
            self.request_map[data] = self.request_ctr
            self.request_ctr += 1
            return self.request_map[data]
        return self.request_map[data]
    
    def _get_message_event_params(self, msg):
        if msg.type == "append_entries_request":
            return {
                "type": "MsgApp",
                "term": msg.parsed_message["term"],
                "from": int(msg.fr),
                "to": int(msg.to),
                "log_term": msg.parsed_message["prev_log_term"], 
                "entries": [{"Term": e["term"], "Data": str(self._get_request_number(e["data"].encode("utf-8")))} for e in msg.parsed_message["entries"] if e["data"] != ""],
                "index": msg.parsed_message["prev_log_idx"],
                "commit": msg.parsed_message["leader_commit"],
                "reject": False,
            }
        elif msg.type == "append_entries_response":
            return {
                "type": "MsgAppResp",
                "term": msg.parsed_message["term"],
                "from": int(msg.fr),
                "to": int(msg.to),
                "log_term": 0, 
                "entries": [],
                "index": msg.parsed_message["current_idx"],
                "commit": 0,
                "reject": msg.parsed_message["success"] == 0,
            }
        elif msg.type == "request_vote_request":
            return {
                "type": "MsgVote",
                "term": msg.parsed_message["term"],
                "from": int(msg.fr),
                "to": int(msg.to),
                "log_term": msg.parsed_message["last_log_term"],
                "entries": [],
                "index": msg.parsed_message["last_log_idx"],
                "commit": 0,
                "reject": False,
            }
        elif msg.type == "request_vote_response":
            return {
                "type": "MsgVoteResp",
                "term": msg.parsed_message["term"],
                "from": int(msg.fr),
                "to": int(msg.to),
                "log_term": 0,
                "entries": [],
                "index": 0,
                "commit": 0,
                "reject": msg.parsed_message["vote_granted"] == 0,
            }
        return {}

    def _handle_message(self, request: Request) -> Response:
        self.logger.debug("Received message: {}".format(request.content))
        msg = Message.from_str(request.content)
        if msg is not None:
            mailbox_key = int(msg.to)
            with self.lock:
                if mailbox_key not in self.mailboxes:
                    self.mailboxes[mailbox_key] = []
                self.mailboxes[mailbox_key].append(msg)
            send_event_params = self._get_message_event_params(msg)
            send_event_params["replica"] = mailbox_key
            self.add_event({"name": "SendMessage", "params": send_event_params})

        return Response.json(HTTPStatus.OK, json.dumps({"message": "Ok"}))
    
    def _map_event_params(self, event):
        if event["type"] == "ClientRequest":
            return {
                "leader": int(event["params"]["leader"]),
                "request": self._get_request_number(event["params"]["request"])
            }
        elif event["type"] == "BecomeLeader":
            return {
                "node": int(event["params"]["node"]),
                "term": int(event["params"]["term"])
            }
        elif event["type"] == "Timeout":
            return {
                "node": int(event["params"]["node"])
            }
        elif event["type"] == "MembershipChange":
            return {
                "action": event["params"]["action"],
                "node": int(event["params"]["node"])
            }
        elif event["type"] == "UpdateSnapshot":
            return {
                "node": int(event["params"]["node"]),
                "snapshot_index": int(event["params"]["snapshot_index"]),
            }
        else:
            return event["params"]
    
    def _handle_event(self, request: Request) -> Response:
        self.logger.debug("Received event: {}".format(request.content))
        event = json.loads(request.content)
        if "replica" in event:
            e = {"name": event["type"], "params": self._map_event_params(event)}
            e["params"]["replica"] = event["replica"]
            with self.lock:
                self.event_trace.append(e)

        return Response.json(HTTPStatus.OK, json.dumps({"message": "Ok"}))

    def wait_for_nodes(self, no_nodes):
        while True:
            done = False
            with self.lock:
                if len(list(self.replicas.keys())) == no_nodes:
                    done = True

            if done:
                break
            time.sleep(0.05)
    
    def get_replicas(self):
        replicas = []
        with self.lock:
            replicas = list(self.replicas.items())

        return replicas
    
    def get_event_trace(self):
        event_trace = []
        with self.lock:
            for e in self.event_trace:
                event_trace.append(e)

        return event_trace
    
    def add_event(self, e):
        with self.lock:
            self.event_trace.append(e)
    
    def schedule_replica(self, replica, max_messages):
        addr = ""
        messages_to_deliver = []
        with self.lock:
            if replica in self.mailboxes and len(self.mailboxes[replica]) > 0:
                for (i,m) in enumerate(self.mailboxes[replica]):
                    if i < max_messages:
                        messages_to_deliver.append(m)
                if len(self.mailboxes[replica]) > max_messages:
                    self.mailboxes[replica] = self.mailboxes[replica][max_messages:]
                else:
                    self.mailboxes[replica] = []
                addr = self.replicas[replica]["addr"]

        for next_msg in messages_to_deliver:
            msg_s = json.dumps(next_msg.to_obj())
            self.logger.debug("Sending message: {} to {}".format(msg_s, addr))
            receive_event_params = self._get_message_event_params(next_msg)
            receive_event_params["replica"] = replica
            self.add_event({"name": "DeliverMessage", "params": receive_event_params})
            try:
                requests.post("http://"+addr+"/message", json=next_msg.to_obj())
            except:
                pass

    def clear_mailboxes(self):
        with self.lock:
            for key in self.mailboxes:
                self.mailboxes[key] = []
            self.event_trace = []
            self.replicas = {}


if __name__ == "__main__":
    import argparse
    import sys
    import time

    parser = argparse.ArgumentParser()
    parser.add_argument('-b', '--bind', default='127.0.0.1',
                        help='bind to this address '
                             '(default: %(default)s)')
    parser.add_argument('-p', '--port', default=7074, type=int, nargs='?',
                        help='bind to this port '
                             '(default: %(default)s)')
    args = parser.parse_args()


    network = Network((args.bind, args.port))
    try:
        network.run()
        while True:
            for r in network.get_replicas():
                network.schedule_replica(r[0], 10)
            time.sleep(0.01)
    except KeyboardInterrupt:
        network.shutdown()
        sys.exit(0)