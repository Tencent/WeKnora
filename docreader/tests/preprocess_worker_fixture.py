"""Real subprocess protocol fixture: no OCR models or network needed."""

import json
import os
from pathlib import Path
import socket
import struct
import sys
import time

from docreader.preprocess import PreprocessLimits, _Channel
from docreader.proto.docreader_pb2 import PreprocessImageResponse


def main():
    connection = _Channel(socket.socket(fileno=int(sys.argv[1])))
    connection.send_bytes(b"ready")
    try:
        if os.environ.get("PREPROCESS_FIXTURE_STALL_INPUT"):
            Path(os.environ["PREPROCESS_FIXTURE_STALL_INPUT"]).write_text(str(os.getpid()))
            while True:
                time.sleep(1)
        while True:
            command = json.loads(connection.recv_bytes())
            if command.get("started"):
                Path(command["started"]).write_text(str(os.getpid()))
            if command.get("crash"):
                os._exit(7)
            if command.get("partial_reply"):
                # A readable socket can contain only the length header. The
                # parent must still enforce its deadline while awaiting data.
                connection.socket.send(struct.pack("!I", 100))
            if command.get("check_address_limit"):
                import resource
                from docreader.preprocess_worker import apply_process_limits

                limits = PreprocessLimits(**json.loads(sys.argv[2]))
                apply_process_limits(limits, int(sys.argv[3]))
                cap = resource.getrlimit(resource.RLIMIT_AS)
                connection.send_bytes(PreprocessImageResponse(
                    image=json.dumps(cap).encode(), status="ok").SerializeToString())
                continue
            if command.get("memory"):
                allocation = bytearray(command["memory"])
                # Touch the pages so RSS supervision sees them on every OS.
                for offset in range(0, len(allocation), 4096):
                    allocation[offset] = 1
            if command.get("release"):
                while not Path(command["release"]).exists():
                    time.sleep(0.01)
            response = PreprocessImageResponse(image=str(os.getpid()).encode(), status="ok")
            connection.send_bytes(response.SerializeToString())
    except (EOFError, BrokenPipeError):
        pass
    finally:
        connection.close()


if __name__ == "__main__":
    main()
