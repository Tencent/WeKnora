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

    def _cleanup(self):
        """Close and unlink the temp file. close() may re-raise a prior flush
        error (ENOSPC), so unlink must still run and cleanup errors must not
        replace the original exception.
        """
        temp_file = getattr(self, "temp_file", None)
        if not temp_file:
            return
        try:
            temp_file.close()
        except OSError:
            pass
        try:
            os.remove(temp_file.name)
        except OSError:
            pass

    def __enter__(self):
        """
        Create file when entering context
        """
        self.temp_file = tempfile.NamedTemporaryFile(suffix=self.suffix, delete=False)
        # If write/flush fails (e.g. ENOSPC), __exit__ is never called because
        # the exception escapes __enter__, so clean up here or the file leaks
        # permanently (delete=False means GC will not remove it either).
        try:
            self.temp_file.write(self.file_content)
            self.temp_file.flush()
        except BaseException:
            self._cleanup()
            raise
        logger.info(
            f"Saved {self.suffix} content to temporary file: {self.temp_file.name}"
        )
        return self.temp_file.name

    def __exit__(self, exc_type, exc_val, exc_tb):
        """
        Delete file when exiting context
        """
        if getattr(self, "temp_file", None):
            name = self.temp_file.name
            self._cleanup()
            logger.info(f"File {name} has been deleted.")
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
        if self.temp_dir:
            # Removing the directory can fail while a reader still holds a file
            # inside it: Windows refuses to unlink an open file, so rmtree
            # leaves a PermissionError behind. As in
            # ``TempFileContext._cleanup``, that failure must not replace the
            # exception the body raised, or the caller loses the real cause to
            # "the process cannot access the file".
            try:
                self.temp_dir.cleanup()
            except OSError as e:
                logger.warning(
                    f"Could not delete temporary directory "
                    f"{self.temp_dir.name}: {e}",
                )
            else:
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
