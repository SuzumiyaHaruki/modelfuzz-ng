
import requests
import logging
import json
from os import path, makedirs
from hashlib import sha256
from .line_coverage import read_report, create_options, SummarizedStats

LOG = logging.getLogger('fuzzer')

class TLCGuider:
    def __init__(self, tlc_addr, record_path) -> None:
        self.tlc_addr = tlc_addr
        self.record_path = record_path
        self.states = {}
    
    def check_new_state(self, trace, event_trace, name, record = False) -> int:
        trace_to_send = event_trace
        trace_to_send.append({"reset": True})
        LOG.debug("Sending trace to TLC: {}".format(trace_to_send))
        try:
            r = requests.post("http://"+self.tlc_addr+"/execute", json=trace_to_send)
            if r.ok:
                response = r.json()
                LOG.debug("Received response from TLC: {}".format(response))               
                new_states = 0
                for i in range(len(response["states"])):
                    tlc_state = {"state": response["states"][i], "key" : response["keys"][i]}
                    if tlc_state["key"] not in self.states:
                        self.states[tlc_state["key"]] = tlc_state
                        new_states += 1
                if record:
                    with open(path.join(self.record_path, name+".log"), "w") as record_file:
                        lines = ["Trace sent to TLC: \n", json.dumps(trace_to_send, indent=2)+"\n\n", "Response received from TLC:\n", json.dumps(response, indent=2)+"\n"]
                        record_file.writelines(lines)
                return new_states
            else:
                LOG.info("Received error response from TLC, code: {}, text: {}".format(r.status_code, r.content))
        except Exception as e:
            LOG.info("Error received from TLC: {}".format(e))
            pass

        return 0
    
    def save(self):
        with open(path.join(self.record_path, "_states.json"), "w") as states_file:
            json.dump(self.states, states_file)

    def restore(self):        
        try:
            states = {}
            with open(path.join(self.record_path, "_states.json")) as states_file:
                states = json.load(states_file)
            self.states = states
        except:
            pass
    
    def coverage(self):
        return len(self.states.keys())

    def reset(self):
        self.states = {}

def create_event_graph(event_trace):
    cur_event = {}
    nodes = {}

    for e in event_trace:
        if "replica" not in e["params"]:
            LOG.info("{}".format(json.dumps(e)))
        replica = e["params"]["replica"]
        node = {"name": e["name"], "params": e["params"], "replica": replica}
        if replica in cur_event:
            node["prev"] = cur_event[replica]["id"]
        id = sha256(json.dumps(node, sort_keys=True).encode('utf-8')).hexdigest()
        node["id"] = id

        cur_event[replica] = node
        nodes[id] = node
    
    return nodes

class TraceGuider(TLCGuider):
    def __init__(self, tlc_addr, record_path) -> None:
        super(TraceGuider, self).__init__(tlc_addr, record_path)
        self.traces = {}

    def check_new_state(self, trace, event_trace, name, record=False) -> int:
        super().check_new_state(trace, event_trace, name, record)

        new = 0
        event_graph = create_event_graph(event_trace)
        event_graph_id = sha256(json.dumps(event_graph, sort_keys=True).encode('utf-8')).hexdigest()

        if event_graph_id not in self.traces:
            self.traces[event_graph_id] = True
            new = 1
        
        return new
    
    def save(self):
        super().save()
        with open(path.join(self.record_path, "_traces.json"), "w") as traces_file:
            json.dump(self.traces, traces_file)
    
    def restore(self):
        try:
            traces = {}
            with open(path.join(self.record_path, "_traces.json")) as traces_file:
                traces = json.load(traces_file)
            self.traces = traces
        except:
            pass

    def reset(self):
        self.traces = {}
        return super().reset()
    
class LineCoverageGuider(TLCGuider):
    def __init__(self, tlc_addr, record_path, root) -> None:
        super(LineCoverageGuider, self).__init__(tlc_addr, record_path)
        self.lines_covered = 0
        self.root = root
    
    def check_new_state(self, trace, event_trace, name, record=False) -> int:
        super().check_new_state(trace, event_trace, name, record)
        old_lines_coverage = self.lines_covered
        LOG.debug("Getting line coverage info from: {}".format(self.root))
        try:
            self.lines_covered = read_linecov_data(self.root)
            LOG.debug("Lines covered: {}".format(self.lines_covered))
        except Exception as e:
            LOG.debug("Error reading line coverage: {}".format(e))
        return self.lines_covered - old_lines_coverage

    def reset(self):
        self.lines_covered = 0
        return super().reset()
    

def read_linecov_data(root):
    parser_options = create_options(root)
    data = read_report(parser_options)

    stats = SummarizedStats.from_covdata(data)
    return stats.line.covered

def get_guider(t, fuzzer_config, config):
    report_path = "fuzzer_report"
    if "report_path" in fuzzer_config:
        report_path = fuzzer_config["report_path"]

    makedirs(report_path, exist_ok=True)
    record_file_prefix = t
    tlc_record_path = path.join(report_path, record_file_prefix+"_traces")
    makedirs(tlc_record_path, exist_ok=True)
    tlc_addr = "127.0.0.1:2023"
    if "tlc_addr" in fuzzer_config:
        tlc_addr = fuzzer_config["tlc_addr"]

    if t == "trace":
        return TraceGuider(tlc_addr, tlc_record_path)
    elif t == "tlc":
        return TLCGuider(tlc_addr, tlc_record_path)
    elif t == "line":
        if config.line_cov_path is not None:
            return LineCoverageGuider(tlc_addr, tlc_record_path, config.line_cov_path)
    return None