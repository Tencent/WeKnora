from urllib.parse import urlsplit

import pytest
from requests import Response

from weknora_mcp_server import WeKnoraClient


@pytest.mark.parametrize(
    ("slug", "path_slug"),
    [
        ("summary/knowledge-id", "summary/knowledge-id"),
        ("concept/a?b#c%20", "concept/a%3Fb%23c%2520"),
    ],
)
def test_wiki_read_page_preserves_the_full_slug(monkeypatch, slug, path_slug):
    client = WeKnoraClient("http://example.test/api/v1", "")

    def send(request, **kwargs):
        url = urlsplit(request.url)
        assert url.path == f"/api/v1/knowledgebase/kb-id/wiki/pages/{path_slug}"
        assert not url.query
        assert not url.fragment
        response = Response()
        response.status_code = 200
        response._content = b'{"success": true}'
        return response

    monkeypatch.setattr(client.session, "send", send)
    assert client.wiki_read_page("kb-id", slug) == {"success": True}
