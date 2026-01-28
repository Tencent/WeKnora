import json
import logging
import time
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Union

import ollama
import requests

from docreader.config import CONFIG

logger = logging.getLogger(__name__)


@dataclass
class ImageUrl:
    """Image URL data structure for caption requests."""

    url: Optional[str] = None
    detail: Optional[str] = None


@dataclass
class Content:
    """Content data structure that can contain text or image URL."""

    type: Optional[str] = None
    text: Optional[str] = None
    image_url: Optional[ImageUrl] = None


@dataclass
class SystemMessage:
    """System message for VLM model requests."""

    role: Optional[str] = None
    content: Optional[str] = None


@dataclass
class UserMessage:
    """User message for VLM model requests, can contain multiple content items."""

    role: Optional[str] = None
    content: List[Content] = field(default_factory=list)


@dataclass
class CompletionRequest:
    """Request structure for VLM model completion API."""

    model: str
    temperature: float
    top_p: float
    messages: List[Union[SystemMessage, UserMessage]]
    user: str


@dataclass
class Model:
    """Model identifier structure."""

    id: str


@dataclass
class ModelsResp:
    """Response structure for available models API."""

    data: List[Model] = field(default_factory=list)


@dataclass
class Message:
    """Message structure in API response."""

    role: Optional[str] = None
    content: Optional[str] = None
    tool_calls: Optional[str] = None


@dataclass
class Choice:
    """Choice structure in API response."""

    message: Optional[Message] = None


@dataclass
class Usage:
    """Token usage information in API response."""

    prompt_tokens: Optional[int] = 0
    total_tokens: Optional[int] = 0
    completion_tokens: Optional[int] = 0


@dataclass
class CaptionChatResp:
    """Response structure for caption chat API."""

    id: Optional[str] = None
    created: Optional[int] = None
    model: Optional[Model] = None
    object: Optional[str] = None
    choices: List[Choice] = field(default_factory=list)
    usage: Optional[Usage] = None

    @staticmethod
    def from_json(json_data: dict) -> "CaptionChatResp":
        """
        Parse API response JSON into a CaptionChatResp object.

        Args:
            json_data: The JSON response from the API

        Returns:
            A parsed CaptionChatResp object
        """
        logger.info("Parsing CaptionChatResp from JSON")
        # Manually parse nested fields with safe field extraction
        choices = []
        for choice in json_data.get("choices", []):
            message_data = choice.get("message", {})
            message = Message(
                role=message_data.get("role"),
                content=message_data.get("content"),
                tool_calls=message_data.get("tool_calls"),
            )
            choices.append(Choice(message=message))

        # Handle usage with safe field extraction
        usage_data = json_data.get("usage", {})
        usage = None
        if usage_data:
            usage = Usage(
                prompt_tokens=usage_data.get("prompt_tokens", 0),
                total_tokens=usage_data.get("total_tokens", 0),
                completion_tokens=usage_data.get("completion_tokens", 0),
            )

        logger.info(
            f"Parsed {len(choices)} choices and usage data: {usage is not None}"
        )
        return CaptionChatResp(
            id=json_data.get("id"),
            created=json_data.get("created"),
            model=json_data.get("model"),
            object=json_data.get("object"),
            choices=choices,
            usage=usage,
        )

    def choice_data(self) -> str:
        """
        Extract the content from the first choice in the response.

        Returns:
            The content string from the first choice, or empty string if no choices
        """
        if (
            not self.choices
            or not self.choices[0]
            or not self.choices[0].message
            or not self.choices[0].message.content
        ):
            logger.warning("No choices available in response")
            return ""
        logger.info("Retrieving content from first choice")
        return self.choices[0].message.content


class Caption:
    """
    Service for generating captions for images using a Vision Language Model.
    Uses an external API to process images and return textual descriptions.
    """

    def __init__(self, vlm_config: Optional[Dict[str, str]] = None):
        """
        Initialize the Caption service with configuration
        from parameters or environment variables.
        """
        logger.info("Initializing Caption service")
        # Default prompt for image captioning in Chinese: "Briefly describe the main content of the image"
        self.prompt = """简单凝炼的描述图片的主要内容"""
        # API request timeout in seconds
        self.timeout = 30

        # Use provided VLM config if available,
        # otherwise fall back to environment variables
        if vlm_config and vlm_config.get("base_url") and vlm_config.get("model_name"):
            # Build completion URL from provided base URL
            self.completion_url = vlm_config.get("base_url", "") + "/chat/completions"
            self.model = vlm_config.get("model_name", "")
            self.api_key = vlm_config.get("api_key", "")
            # Interface type: "ollama" or "openai" (default)
            self.interface_type = vlm_config.get("interface_type", "openai").lower()
        else:
            # Fall back to environment variables if config not provided
            self.completion_url = CONFIG.vlm_model_base_url + "/chat/completions"
            self.model = CONFIG.vlm_model_name
            self.api_key = CONFIG.vlm_model_api_key
            self.interface_type = CONFIG.vlm_interface_type

        # Validate interface type - must be either "ollama" or "openai"
        if self.interface_type not in ["ollama", "openai"]:
            logger.warning(
                f"Unknown interface type: {self.interface_type}, defaulting to openai"
            )
            self.interface_type = "openai"

        logger.info(
            f"Configured with model: {self.model}, "
            f"endpoint: {self.completion_url}, interface: {self.interface_type}"
        )

    def _call_caption_api(self, image_data: str) -> Optional[CaptionChatResp]:
        """
        Call the Caption API to generate a description for the given image.

        Args:
            image_data: URL of the image or base64 encoded image data

        Returns:
            CaptionChatResp object if successful, None otherwise
        """
        logger.info("Calling Caption API for image captioning")
        logger.info(f"Processing image data: {image_data[:50]}...")

        # Route to appropriate API based on interface type
        if self.interface_type == "ollama":
            return self._call_ollama_api(image_data)
        else:
            return self._call_openai_api(image_data)

    def _call_ollama_api(self, image_base64: str) -> Optional[CaptionChatResp]:
        """Call Ollama API for image captioning using base64 encoded image data."""

        # Extract host URL by removing the chat completions endpoint
        # Handle both "/v1/chat/completions" and "/chat/completions" patterns
        host = self.completion_url.replace("/v1/chat/completions", "").replace("/chat/completions", "")

        # Initialize Ollama client with host and timeout
        client = ollama.Client(
            host=host,
            timeout=self.timeout,
        )

        try:
            logger.info(f"Calling Ollama API with model: {self.model}")

            # Call Ollama API with base64 encoded image
            # Prompt: "Briefly describe the main content of the image"
            response = client.generate(
                model=self.model,
                prompt="简单凝炼的描述图片的主要内容",
                images=[image_base64],  # Pass base64 encoded image data
                options={
                    "temperature": 0.1
                },  # Low temperature for more deterministic output
                stream=False,
            )

            # Construct response object in standard format
            caption_resp = CaptionChatResp(
                id="ollama_response",
                created=int(time.time()),
                model=Model(id=self.model),
                object="chat.completion",
                choices=[
                    Choice(message=Message(role="assistant", content=response.response))
                ],
            )

            logger.info("Successfully received response from Ollama API")
            return caption_resp

        except Exception as e:
            logger.error(f"Error calling Ollama API: {e}")
            return None

    def _call_openai_api(self, image_base64: str) -> Optional[CaptionChatResp]:
        """Call OpenAI-compatible API for image captioning."""
        logger.info(f"Calling OpenAI-compatible API with model: {self.model}")

        # Construct user message with text prompt and base64 encoded image
        user_msg = UserMessage(
            role="user",
            content=[
                Content(type="text", text=self.prompt),
                Content(
                    type="image_url",
                    image_url=ImageUrl(
                        url="data:image/png;base64," + image_base64, detail="auto"
                    ),
                ),
            ],
        )

        # Build completion request with model parameters
        gpt_req = CompletionRequest(
            model=self.model,
            temperature=0.3,  # Moderate randomness for balanced output
            top_p=0.8,  # Nucleus sampling parameter
            messages=[user_msg],
            user="abc",
        )

        # Set up HTTP headers for the API request
        headers = {
            "Content-Type": "application/json",
            "Accept": "text/event-stream",
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
        }
        # Add authorization header if API key is provided
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"

        try:
            logger.info(
                f"Sending request to OpenAI-compatible API with model: {self.model}"
            )
            # Send POST request to the API endpoint
            response = requests.post(
                self.completion_url,
                data=json.dumps(gpt_req, default=lambda o: o.__dict__, indent=4),
                headers=headers,
                timeout=self.timeout,
            )
            # Check for successful response
            if response.status_code != 200:
                logger.error(
                    f"OpenAI API returned non-200 status code: {response.status_code}"
                )
                response.raise_for_status()

            logger.info(f"Received from OpenAI with status: {response.status_code}")
            logger.info("Converting response to CaptionChatResp object")
            # Parse JSON response into structured object
            caption_resp = CaptionChatResp.from_json(response.json())

            if caption_resp.usage:
                logger.info(
                    f"API usage: prompt_tokens={caption_resp.usage.prompt_tokens}, "
                    f"completion_tokens={caption_resp.usage.completion_tokens}"
                )

            return caption_resp
        except requests.exceptions.Timeout:
            logger.error("Timeout while calling OpenAI-compatible API after 30 seconds")
            return None
        except requests.exceptions.RequestException as e:
            logger.error(f"Request error calling OpenAI-compatible API: {e}")
            return None
        except Exception as e:
            logger.error(f"Unexpected error calling OpenAI-compatible API: {e}")
            return None

    def get_caption(self, image_data: str) -> str:
        """
        Get a caption for the provided image data.

        Args:
            image_data: URL of the image or base64 encoded image data

        Returns:
            Caption text as string, or empty string if captioning failed
        """
        logger.info("Getting caption for image")
        if not image_data or self.completion_url is None:
            logger.error("Image data is not set")
            return ""
        caption_resp = self._call_caption_api(image_data)
        if caption_resp:
            caption = caption_resp.choice_data()
            caption_length = len(caption)
            logger.info(f"Successfully generated caption of length {caption_length}")
            logger.info(
                f"Caption: {caption[:50]}..."
                if caption_length > 50
                else f"Caption: {caption}"
            )
            return caption
        logger.warning("Failed to get caption from Caption API")
        return ""
