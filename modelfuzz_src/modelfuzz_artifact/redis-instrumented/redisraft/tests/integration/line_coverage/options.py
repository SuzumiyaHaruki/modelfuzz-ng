from typing import Any
import os
from .utils import Filter, AlwaysMatchFilter

class Options(object):
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)

    def get(self, name: str) -> Any:
        """Function to get an option by name."""
        return self.__dict__.get(name)
    

def create_options(root: str) -> Options:
    root_dir = os.path.abspath(root)
    return Options(**{
        "root": root,
        "starting_dir": os.path.abspath(os.getcwd()),
        "root_dir": root_dir,
        "gcov_files": None,
        "search_paths": [],
        "gcov_objdir": None,
        "gcov_exclude_dirs": [],
        "gcov_parallel": 2,
        "source_encoding": 'utf-8',
        "gcov_objdir": None,
        "filter": [AlwaysMatchFilter()],
        "exclude": [],
        "gcov_ignore_parse_errors": None,
        "gcov_ignore_errors": None,
        "gcov_delete": False,
        "gcov_cmd": os.environ.get("GCOV", "gcov"),
        "gcov_filter": [AlwaysMatchFilter()],
        "gcov_exclude": [],
        "gcov_keep": False,
        "merge_mode_functions": "strict",
    })