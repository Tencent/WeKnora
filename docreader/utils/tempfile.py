import logging
import os
import tempfile

logger = logging.getLogger(__name__)


class TempFileContext:
    def __init__(self, file_content: bytes, suffix: str):
        """
        Initialize the context
        :param file_content: Byte data to write to file
        :param suffix: File suffix
        """
        self.file_content = file_content
        self.suffix = suffix
        self.file = None

    def __enter__(self):
        """
        Create file when entering context
        """
        self.temp_file = tempfile.NamedTemporaryFile(suffix=self.suffix, delete=False)
        self.temp_file.write(self.file_content)
        self.temp_file.flush()
        logger.info(
            f"Saved {self.suffix} content to temporary file: {self.temp_file.name}"
        )
        return self.temp_file.name

    def __exit__(self, exc_type, exc_val, exc_tb):
        """
        Delete file when exiting context
        """
        if self.temp_file:
            self.temp_file.close()
            if os.path.exists(self.temp_file.name):
                os.remove(self.temp_file.name)
            logger.info(f"File {self.temp_file.name} has been deleted.")
        # Return False to propagate exception (if any exception occurred)
        return False


class TempDirContext:
    def __init__(self):
        """
        Initialize the context
        """
        self.temp_dir = None

    def __enter__(self):
        """
        Create directory when entering context
        """
        self.temp_dir = tempfile.TemporaryDirectory()
        logger.info(f"Created temporary directory: {self.temp_dir.name}")
        return self.temp_dir.name

    def __exit__(self, exc_type, exc_val, exc_tb):
        """
        Delete directory when exiting context
        """
        if self.temp_dir and os.path.exists(self.temp_dir.name):
            self.temp_dir.cleanup()
            logger.info(f"Directory {self.temp_dir.name} has been deleted.")
        # Return False to propagate exception (if any exception occurred)
        return False


if __name__ == "__main__":
    example_bytes = b"Hello, this is a test file."
    file_name = "test_file.txt"

    # Using with statement
    with TempFileContext(example_bytes, file_name) as temp_file:
        # File operations can be performed within the context
        print(f"Does file {file_name} exist: {os.path.exists(file_name)}")
