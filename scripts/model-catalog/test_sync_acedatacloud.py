import unittest

from sync_acedatacloud import project, update


def row(model, protocol="chat_completions", **extra):
    path = {"chat_completions": "/v1/chat/completions", "messages": "/v1/messages", "responses": "/v1/responses"}[protocol]
    return {"id": model, "label": model, "type": "chat", "capabilities": [], "endpoints": [{
        "protocol": protocol, "path": path, "auth": "bearer",
        "base_url": "https://api.acedata.cloud" + ("" if protocol == "messages" else "/v1"),
    }], **extra}


class CatalogTest(unittest.TestCase):
    def test_protocols_and_typed_modalities_are_authoritative(self):
        entries = project({"data": [row("gpt-4o-image", capabilities=["vision"]), row("native", "messages"), row("response-only", "responses"), row("not-chat", type="image")]})
        self.assertEqual([m["id"] for m in entries], ["gpt-4o-image", "native", "response-only"])
        self.assertEqual([m["api"] for m in entries], ["openai-completions", "anthropic-messages", "openai-responses"])
        self.assertEqual(entries[0]["input"], ["text", "image"])

    def test_unknown_or_untrusted_protocol_cannot_create_partial_catalog(self):
        for invalid in [[], [{"protocol": "chat_completions", "path": "/v1/chat/completions", "auth": "bearer", "base_url": "https://other.example/v1"}]]:
            with self.assertRaises(ValueError):
                project({"data": [row("valid"), row("bad", endpoints=invalid)]})
        with self.assertRaises(ValueError):
            project({"data": [row("same"), row("same")]})

    def test_host_prices_limits_and_effort_levels_are_not_inferred(self):
        entry = project({"data": [row("gpt-4.1", context_window=1000000, pricing={"input": 0}, thinking_levels={"high": "high"})]})[0]
        self.assertFalse({"context_window", "cost", "thinking_levels", "max_output_tokens"} & entry.keys())

    def test_retired_suggestions_preserve_saved_model_resolution(self):
        seed = {"version": 1, "providers": {"other": [{"id": "untouched"}], "acedatacloud": [{"id": "old", "api": "openai-completions"}]}}
        result = update(seed, project({"data": [row("new")]}))
        self.assertEqual(result["providers"]["other"], seed["providers"]["other"])
        self.assertEqual(result["providers"]["acedatacloud"][1], {"id": "old", "api": "openai-completions", "deprecated": True})
        self.assertNotIn("deprecated", seed["providers"]["acedatacloud"][0])
        self.assertEqual(update(result, project({"data": [row("new")]})), result)


if __name__ == "__main__":
    unittest.main()
