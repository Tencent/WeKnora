"""Generate the README artwork in the product website's visual language.

Outputs hero-*.svg, capabilities-*.svg, architecture-*.svg (per language and theme) and
icons/*.svg into the directory above this script. Colors and fonts follow
website-docs/shared/brand.css; line icons come from website-docs/homepage/app/ui.tsx.
The wordmark PNGs are docs/images/logo.png cropped, with the white ground turned
into alpha (dark variant recolors the navy lettering and keeps the gold).

    python3 docs/images/readme/src/generate.py
"""
import base64, io, os
from PIL import Image, ImageFont
import numpy as np

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.dirname(HERE)
os.makedirs(OUT, exist_ok=True)

THEMES = {
    "light": dict(panel="#fdfcfa", card="#ffffff", rule="rgba(16,31,56,.12)", rule_soft="rgba(16,31,56,.07)",
                  ink="#101f38", soft="#40536f", mute="#6d7d94", gold="#b8863b", accent="#886025",
                  chip="#dfd1b9", chip_bg="#faf6ee", icon_bg="#f6f1e7"),
    "dark": dict(panel="#0b1220", card="#101a2b", rule="rgba(190,208,232,.14)", rule_soft="rgba(190,208,232,.08)",
                 ink="#e6ecf5", soft="#a9b6c9", mute="#9aaac0", gold="#d9a94f", accent="#e8c176",
                 chip="#685638", chip_bg="#161f30", icon_bg="#1a2436"),
}
LATIN_SANS = "Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI'"
LATIN_SERIF = "'Iowan Old Style', 'Palatino Linotype', Palatino, 'Book Antiqua', Georgia"
CJK_SANS = {
    "cn": "'PingFang SC', 'Hiragino Sans GB', 'Noto Sans CJK SC', 'Microsoft YaHei'",
    "ja": "'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Yu Gothic', Meiryo, 'Noto Sans CJK JP'",
    "ko": "'Apple SD Gothic Neo', 'Malgun Gothic', 'Noto Sans CJK KR'",
}
CJK_SERIF = {
    "cn": "'Songti SC', 'Source Han Serif SC', 'Noto Serif CJK SC', STSong",
    "ja": "'Hiragino Mincho ProN', 'Yu Mincho', 'Noto Serif CJK JP'",
    "ko": "AppleMyungjo, 'Nanum Myeongjo', 'Noto Serif CJK KR'",
}


def sans(lang):
    return f"{LATIN_SANS}, {CJK_SANS.get(lang, CJK_SANS['cn'])}, sans-serif"


def serif(lang):
    return f"{LATIN_SERIF}, {CJK_SERIF.get(lang, CJK_SERIF['cn'])}, serif"


MONO = "'JetBrains Mono', SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace"

# Line icons copied from website-docs/homepage/app/ui.tsx (24x24, stroke).
ICONS = {
    "search": '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5M8 8h5M8 11h3"/>',
    "agent": '<rect x="5" y="7" width="14" height="13" rx="3"/><path d="M12 3v4M2 11v5m20-5v5M9 12v1m6-1v1m-6 4h6"/><circle cx="12" cy="2.5" r=".5"/>',
    "wiki": '<path d="M12 6C9 3 5 3 2 4v15c4-1 7 0 10 2 3-2 6-3 10-2V4c-3-1-7-1-10 2v15"/><path d="M5 8h3m-3 4h3m8-4h3m-3 4h3"/>',
    "github": '<path d="M9 19c-4.3 1.3-4.3-2.2-6-2.7m12 5v-3.5c0-1 .1-1.4-.5-2 3.4-.4 7-1.7 7-7.5a5.7 5.7 0 0 0-1.5-4 5.4 5.4 0 0 0-.1-4S18.6-.1 15.5 2a14 14 0 0 0-7 0C5.4-.1 4.1.3 4.1.3A5.4 5.4 0 0 0 4 4.3a5.7 5.7 0 0 0-1.5 4c0 5.8 3.6 7.1 7 7.5-.6.6-.6 1.2-.5 2V21"/>',
    "server": '<rect x="3" y="3" width="18" height="7" rx="1"/><rect x="3" y="14" width="18" height="7" rx="1"/><path d="M7 6.5h.01M7 17.5h.01M12 6.5h5m-5 11h5"/>',
    "model": '<path d="m12 2 10 5-10 5L2 7l10-5Zm-10 10 10 5 10-5M2 17l10 5 10-5"/>',
}

try:  # Only used to measure latin text for line wrapping.
    LATIN = ImageFont.truetype("/System/Library/Fonts/Supplemental/Arial.ttf", 100)
except OSError:
    LATIN = ImageFont.truetype("Arial.ttf", 100)


def is_wide(ch):
    return ord(ch) >= 0x2E80


def breaks_anywhere(ch):
    """Han, kana and CJK punctuation can break between any two characters; Hangul breaks at spaces."""
    return is_wide(ch) and not 0xAC00 <= ord(ch) <= 0xD7AF


def text_width(s, size, mono=False):
    w = 0.0
    for ch in s:
        if 0xAC00 <= ord(ch) <= 0xD7AF:  # Hangul syllables render narrower than Han
            w += size * 0.9
        elif is_wide(ch):
            w += size
        elif mono:
            w += size * 0.6
        else:
            w += LATIN.getlength(ch) / 100 * size
    return w


def wrap(s, size, width):
    """Greedy wrap: Han and kana break anywhere, latin and Hangul break at spaces."""
    tokens, buf, group = [], "", ""
    for ch in s:
        if group or ch in "（(":  # keep a bracketed aside like （任意） on one line
            if not group and buf:
                tokens.append(buf); buf = ""
            group += ch
            if ch in "）)":
                tokens.append(group); group = ""
            continue
        if breaks_anywhere(ch):
            if buf:
                tokens.append(buf); buf = ""
            tokens.append(ch)
        elif ch == " ":
            tokens.append(buf + " "); buf = ""
        else:
            buf += ch
    if buf or group:
        tokens.append(buf + group)
    lines, cur = [], ""
    for t in tokens:
        if cur and text_width((cur + t).rstrip(), size) > width and t not in "，。、；：）」":
            carry = ""
            while cur and cur[-1] in "（(「":
                carry, cur = cur[-1] + carry, cur[:-1]
            lines.append(cur.rstrip()); cur = carry + t.lstrip()
        else:
            cur += t
    if cur:
        lines.append(cur.rstrip())
    return lines


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def icon(name, x, y, size, color, width=1.4):
    k = size / 24
    return (f'<g transform="translate({x} {y}) scale({k})" fill="none" stroke="{color}" stroke-width="{width}" '
            f'stroke-linecap="round" stroke-linejoin="round">{ICONS[name]}</g>')


def wordmark(theme):
    img = Image.open(os.path.join(HERE, f"wordmark-{theme}.png"))
    img = img.resize((452, 104), Image.LANCZOS)
    buf = io.BytesIO(); img.save(buf, "PNG", optimize=True)
    return "data:image/png;base64," + base64.b64encode(buf.getvalue()).decode()


COPY = {
    "cn": dict(
        eyebrow="TENCENT OPEN SOURCE · WEKNORA",
        h1=["帮你找到答案，", "并将知识付诸实践。"], h1_size=88,
        desc=["腾讯开源的企业级知识管理框架。", "汇集团队资料，用于知识问答、任务执行和 Wiki 整理。"],
        highlights=["RAG 问答", "Agent 推理", "自动 Wiki", "本机浏览器", "技能与沙箱", "MCP Server", "长期记忆", "数据源与 IM"],
        trust=[("github", "腾讯开源 · MIT License"), ("server", "支持私有化部署"), ("model", "自由选择模型与存储")],
        cards=[
            ("search", "01 / RAG", "回答有据可查", "结合语义与关键词检索查找相关资料，回答附带来源引用，可打开原文核对。", ["混合检索", "多模态解析", "原文引用"]),
            ("agent", "02 / AGENT", "用知识和工具完成任务", "智能体根据任务检索知识库、搜索网页、调用 MCP 工具与技能，在沙箱中处理文件、运行脚本，还能操作你电脑上的浏览器，并跨会话记住你确认过的偏好。", ["多步推理", "技能与沙箱", "本机浏览器", "MCP 工具", "长期记忆"]),
            ("wiki", "03 / WIKI", "把文档整理成 Wiki", "从原始文档生成相互链接的 Wiki 页面与知识图谱，支持浏览、编辑和版本回滚。", ["自动组织", "知识图谱", "版本回滚"]),
        ],
    ),
    "en": dict(
        eyebrow="TENCENT OPEN SOURCE · WEKNORA",
        h1=["Find the answers,", "and put knowledge to work."], h1_size=68,
        desc=["Tencent's open-source knowledge management framework.", "Bring team documents together for Q&A, tasks and wikis."],
        highlights=["RAG Q&A", "Agent reasoning", "Auto Wiki", "Local browser", "Skills & sandbox", "MCP Server", "Long-term memory", "Sources & IM"],
        trust=[("github", "Tencent Open Source · MIT License"), ("server", "Self-hosted deployment"), ("model", "Your choice of models and storage")],
        cards=[
            ("search", "01 / RAG", "Answers you can check", "Semantic and keyword search find the relevant material. Every answer cites its sources, and you can open the original to verify.", ["Hybrid search", "Multimodal parsing", "Citations"]),
            ("agent", "02 / AGENT", "Tasks done with knowledge and tools", "The agent searches knowledge bases and the web, calls MCP tools and skills, handles files and scripts in a sandbox, operates the browser on your computer, and remembers preferences you have confirmed.", ["Multi-step", "Skills & sandbox", "Local browser", "MCP tools", "Memory"]),
            ("wiki", "03 / WIKI", "Documents organized into a wiki", "Builds interlinked wiki pages and a knowledge graph from raw documents, with browsing, editing and version rollback.", ["Auto-organized", "Knowledge graph", "Rollback"]),
        ],
    ),
    "ja": dict(
        eyebrow="TENCENT OPEN SOURCE · WEKNORA",
        h1=["答えを見つけ、", "知識を実践に活かす。"], h1_size=88,
        desc=["Tencent が公開するオープンソースのエンタープライズ向けナレッジ管理フレームワーク。", "チームの資料を集約し、Q&A・タスク実行・Wiki 整理に活用します。"],
        highlights=["RAG Q&A", "Agent 推論", "自動 Wiki", "ローカルブラウザ", "サンドボックス", "MCP Server", "長期メモリ", "データ連携 · IM"],
        trust=[("github", "Tencent オープンソース · MIT License"), ("server", "プライベートデプロイ対応"), ("model", "モデルとストレージを自由に選択")],
        cards=[
            ("search", "01 / RAG", "根拠のある回答", "セマンティック検索とキーワード検索を組み合わせて関連資料を探し、回答には出典が付きます。原文を開いて確認できます。", ["ハイブリッド検索", "マルチモーダル解析", "原文引用"]),
            ("agent", "02 / AGENT", "知識とツールでタスクを完了", "エージェントがナレッジベースや Web を検索し、MCP ツールやスキルを呼び出し、サンドボックスでファイル処理やスクリプト実行を行い、お使いのパソコンのブラウザも操作します。確認済みの好みはセッションをまたいで記憶します。", ["マルチステップ推論", "サンドボックス", "ローカルブラウザ", "MCP ツール", "長期メモリ"]),
            ("wiki", "03 / WIKI", "ドキュメントを Wiki に整理", "原文書から相互リンクされた Wiki ページとナレッジグラフを生成し、閲覧・編集・バージョンのロールバックに対応します。", ["自動整理", "ナレッジグラフ", "ロールバック"]),
        ],
    ),
    "ko": dict(
        eyebrow="TENCENT OPEN SOURCE · WEKNORA",
        h1=["답을 찾고,", "지식을 실천으로."], h1_size=88,
        desc=["Tencent가 오픈소스로 공개한 엔터프라이즈 지식 관리 프레임워크입니다.", "팀 자료를 모아 Q&A, 작업 실행, Wiki 정리에 활용합니다."],
        highlights=["RAG Q&A", "Agent 추론", "자동 Wiki", "로컬 브라우저", "스킬과 샌드박스", "MCP Server", "장기 메모리", "데이터 소스와 IM"],
        trust=[("github", "Tencent 오픈소스 · MIT License"), ("server", "프라이빗 배포 지원"), ("model", "모델과 스토리지 자유 선택")],
        cards=[
            ("search", "01 / RAG", "근거 있는 답변", "시맨틱 검색과 키워드 검색으로 관련 자료를 찾고, 답변에 출처를 표시합니다. 원문을 열어 확인할 수 있습니다.", ["하이브리드 검색", "멀티모달 파싱", "원문 인용"]),
            ("agent", "02 / AGENT", "지식과 도구로 작업 완료", "에이전트가 작업에 맞춰 지식베이스와 웹을 검색하고 MCP 도구와 스킬을 호출하며, 샌드박스에서 파일을 처리하고 스크립트를 실행하고, 사용자 컴퓨터의 브라우저도 조작합니다. 확인한 선호는 세션을 넘어 기억합니다.", ["다단계 추론", "스킬과 샌드박스", "로컬 브라우저", "MCP 도구", "장기 메모리"]),
            ("wiki", "03 / WIKI", "문서를 Wiki로 정리", "원본 문서에서 상호 연결된 Wiki 페이지와 지식 그래프를 생성하고, 탐색·편집·버전 롤백을 지원합니다.", ["자동 정리", "지식 그래프", "버전 롤백"]),
        ],
    ),
}


def hero(lang, theme):
    c, t = COPY[lang], THEMES[theme]
    W = 1600
    l2 = 208 + 2 * c["h1_size"] + 44
    desc_y = l2 + 72
    rule_y = desc_y + 42 * (len(c["desc"]) - 1) + 56
    H = rule_y + 104
    o = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="WeKnora">',
         f'<rect x="1" y="1" width="{W-2}" height="{H-2}" rx="20" fill="{t["panel"]}" stroke="{t["rule"]}" stroke-width="2"/>',
         f'<image href="{wordmark(theme)}" x="72" y="60" width="226" height="52"/>']
    # eyebrow + headline + description
    o += [f'<text x="72" y="208" font-family="{MONO}" font-size="20" letter-spacing="3" fill="{t["gold"]}">{c["eyebrow"]}</text>',
          f'<text x="68" y="{208+c["h1_size"]+22}" font-family="{serif(lang)}" font-size="{c["h1_size"]}" fill="{t["ink"]}">{esc(c["h1"][0])}</text>',
          f'<text x="68" y="{208+2*c["h1_size"]+44}" font-family="{serif(lang)}" font-size="{c["h1_size"]}" fill="{t["gold"]}">{esc(c["h1"][1])}</text>']
    for i, line in enumerate(c["desc"]):
        o.append(f'<text x="72" y="{desc_y+i*42}" font-family="{sans(lang)}" font-size="27" fill="{t["soft"]}">{esc(line)}</text>')
    # what's inside: the three core modes first, then five more highlights, 2 x 4
    gw, gh, gx_gap, gy_gap = 256, 62, 14, 12
    gx0, gy0 = W - 72 - 2 * gw - gx_gap, 178
    for i, label in enumerate(c["highlights"]):
        x = gx0 + (i % 2) * (gw + gx_gap)
        y = gy0 + (i // 2) * (gh + gy_gap)
        fit(label, label, 20, gw - 82)
        o += [f'<rect x="{x}" y="{y}" width="{gw}" height="{gh}" rx="12" fill="{t["card"]}" stroke="{t["rule"]}" stroke-width="2"/>',
              f'<rect x="{x+11}" y="{y+11}" width="40" height="40" rx="9" fill="{t["icon_bg"]}"/>',
              icon(HERO_ICONS[i], x + 19, y + 19, 24, t["gold"], 1.6),
              f'<text x="{x+64}" y="{y+38}" font-family="{sans(lang)}" font-size="20" font-weight="600" fill="{t["ink"]}">{esc(label)}</text>']
    # trust bar
    o.append(f'<path d="M72 {rule_y}H{W-72}" stroke="{t["rule_soft"]}" stroke-width="2"/>')
    col = (W - 144) / 3
    for i, (ic, label) in enumerate(c["trust"]):
        x = 72 + i * col
        o += [icon(ic, x, H - 70, 26, t["mute"], 1.6),
              f'<text x="{x+40}" y="{H-49}" font-family="{sans(lang)}" font-size="22" fill="{t["soft"]}">{esc(label)}</text>']
    o.append("</svg>")
    return "\n".join(o)


def cards(lang, theme):
    c, t = COPY[lang], THEMES[theme]
    W, gap, pad = 1600, 28, 40
    cw = (W - 2 * gap) / 3
    tw = cw - 2 * pad
    desc_size, title_size = 24, 33
    layouts = []
    for ic, label, title, desc, tags in c["cards"]:
        tl = wrap(title, title_size, tw)
        dl = wrap(desc, desc_size, tw)
        # tag rows
        rows, cur, cur_w = [], [], 0
        for tag in tags:
            w = text_width(tag, 20) + 32
            if cur and cur_w + w > tw:
                rows.append(cur); cur, cur_w = [], 0
            cur.append((tag, w)); cur_w += w + 10
        rows.append(cur)
        layouts.append((ic, label, tl, dl, rows))
    body_h = max(len(l[2]) * 44 + len(l[3]) * 40 for l in layouts)
    tag_h = max(len(l[4]) for l in layouts) * 50
    H = int(pad + 56 + 44 + body_h + 36 + tag_h + pad)
    o = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img">']
    for i, (ic, label, tl, dl, rows) in enumerate(layouts):
        x = i * (cw + gap)
        o += [f'<rect x="{x+1}" y="1" width="{cw-2}" height="{H-2}" rx="18" fill="{t["card"]}" stroke="{t["rule"]}" stroke-width="2"/>',
              f'<rect x="{x+pad}" y="{pad}" width="56" height="56" rx="12" fill="{t["icon_bg"]}"/>',
              icon(ic, x + pad + 12, pad + 12, 32, t["gold"], 1.5),
              f'<text x="{x+cw-pad}" y="{pad+34}" text-anchor="end" font-family="{MONO}" font-size="19" letter-spacing="2" fill="{t["gold"]}">{label}</text>']
        y = pad + 56 + 56
        for line in tl:
            o.append(f'<text x="{x+pad}" y="{y}" font-family="{sans(lang)}" font-size="{title_size}" font-weight="600" fill="{t["ink"]}">{esc(line)}</text>')
            y += 44
        y += 4
        for line in dl:
            o.append(f'<text x="{x+pad}" y="{y}" font-family="{sans(lang)}" font-size="{desc_size}" fill="{t["soft"]}">{esc(line)}</text>')
            y += 40
        ty = H - pad - tag_h + 6
        for row in rows:
            tx = x + pad
            for tag, w in row:
                o += [f'<rect x="{tx}" y="{ty}" width="{w}" height="38" rx="7" fill="{t["chip_bg"]}" stroke="{t["chip"]}" stroke-width="1.5"/>',
                      f'<text x="{tx+16}" y="{ty+26}" font-family="{sans(lang)}" font-size="20" fill="{t["accent"]}">{esc(tag)}</text>']
                tx += w + 10
            ty += 50
    o.append("</svg>")
    return "\n".join(o)


# ---------------------------------------------------------------------------
# Architecture diagram
# ---------------------------------------------------------------------------

HERO_ICONS = ["search", "agent", "wiki", "browser", "sandbox", "plug", "memory", "sources"]

ICONS.update({
    "memory": '<path d="M12 7c-1-5-7-5-7 0-4 1-4 7 0 8-1 5 5 7 7 3 2 4 8 2 7-3 4-1 4-7 0-8 0-5-6-5-7 0Zm0 0v11M5 7c0 2 2 3 3 3m11-3c0 2-2 3-3 3M5 15l3-1m11 1-3-1"/>',
    "monitor": '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8m-4-4v4M6 7h5m-5 3h3"/>',
    "terminal": '<rect x="2" y="3" width="20" height="18" rx="2"/><path d="m6 8 4 4-4 4m7 0h5"/>',
    "channels": '<rect x="7" y="2" width="10" height="7" rx="1"/><rect x="2" y="16" width="8" height="6" rx="1"/><rect x="14" y="16" width="8" height="6" rx="1"/><path d="M12 9v4m-6 3v-3h12v3"/>',
    "code": '<path d="m7 7-5 5 5 5m10-10 5 5-5 5m-3-14-4 18"/>',
    "plug": '<path d="M9 2v5m6-5v5M6 7h12v4a6 6 0 0 1-12 0V7Zm6 10v5"/>',
    "browser": '<rect x="2" y="4" width="20" height="16" rx="2"/><path d="M2 9h20M6 6.5h.01M9 6.5h.01"/><path d="m11 13 6 2.5-2.6.9-.9 2.6Z"/>',
    "file": '<path d="M14 2H5v20h14V7l-5-5Zm0 0v5h5M8 12h8m-8 4h6"/>',
    "sandbox": '<path d="m12 2 9 5v10l-9 5-9-5V7l9-5Zm0 10 9-5M12 12 3 7m9 5v10"/><path d="m7 4.8 10 5.5"/>',
    "sources": '<path d="M3 7h7l2 3h9v10H3V7Zm3 0V3h11l3 3v4"/><path d="M7 14h10m-10 3h6"/>',
    "shield": '<path d="m12 2 9 4v6c0 5-5 8-9 10-4-2-9-5-9-10V6l9-4Z"/><path d="m8 12 3 3 5-6"/>',
    "trace": '<path d="M3 3v18h18M6 15l4-5 4 3 6-8"/><circle cx="10" cy="10" r="1"/><circle cx="14" cy="13" r="1"/>',
    "db": '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
    "chevron": '<path d="m9 6 6 6-6 6"/>',
})

ARCH = {
    "cn": dict(
        heads=["客户端与渠道", "WeKnora 主服务", "运行时服务", "存储"],
        access=[("monitor", "Web UI", "Vue 3 · HTTP / SSE"), ("terminal", "API · CLI · SDK", "API Key 按能力授权"),
                ("channels", "IM 频道", "企业微信 · 飞书 · Slack 等"), ("code", "网站嵌入", "Widget · 域名白名单"),
                ("plug", "MCP 客户端", "Streamable HTTP"), ("browser", "本机浏览器", "BrowserSkill · WebSocket")],
        modes=[("search", "RAG 问答", ["查询理解", "混合检索", "重排序", "流式生成 · 引用"]),
               ("agent", "Agent 推理", ["ReAct 多步推理", "工具 · 技能 · MCP", "沙箱执行", "长期记忆"]),
               ("wiki", "自动 Wiki", ["页面生成与维护", "知识图谱", "人工编辑", "版本回滚"])],
        shared="三种能力共享同一知识库",
        pipeline_head="知识处理流水线",
        pipeline=["导入", "解析", "分块", "向量化", "索引", "富化"],
        pipeline_note="摘要、问题、图谱与标签由异步任务队列分阶段处理",
        platform=[("shield", "空间与权限", "RBAC · 审计日志"), ("model", "模型目录", "27 家厂商"),
                  ("server", "任务队列", "Worker 池治理"), ("trace", "可观测性", "Langfuse 追踪")],
        services=[("file", "文档解析", "docreader · gRPC"), ("sandbox", "会话沙箱", "Docker · E2B · Cube"),
                  ("model", "模型服务", "27 家厂商 · Ollama"), ("search", "联网搜索", "Bing · 博查 · SearXNG 等"),
                  ("plug", "外部 MCP 服务", "工具 · OAuth2"), ("sources", "数据源", "飞书 · Confluence 等")],
        stores=[("PostgreSQL", "业务数据 · BM25 · pgvector"), ("Redis", "任务队列 · 流 · 限流"),
                ("向量库", "ES · Qdrant · Milvus 等（可选）"), ("对象存储", "本地 · MinIO · COS · S3 等"),
                ("Neo4j", "知识图谱（可选）"), ("Langfuse", "链路追踪（可选）")],
    ),
    "en": dict(
        heads=["Clients and channels", "WeKnora app", "Runtime services", "Storage"],
        access=[("monitor", "Web UI", "Vue 3 · HTTP / SSE"), ("terminal", "API · CLI · SDK", "Scoped API keys"),
                ("channels", "IM channels", "WeCom · Feishu · Slack …"), ("code", "Website embed", "Widget · domain allowlist"),
                ("plug", "MCP clients", "Streamable HTTP"), ("browser", "Local browser", "BrowserSkill · WebSocket")],
        modes=[("search", "RAG Q&A", ["Query understanding", "Hybrid retrieval", "Reranking", "Cited answers"]),
               ("agent", "Agent reasoning", ["ReAct multi-step", "Tools · skills · MCP", "Sandbox execution", "Long-term memory"]),
               ("wiki", "Auto Wiki", ["Page generation", "Knowledge graph", "Manual edits", "Version rollback"])],
        shared="All three share the same knowledge bases",
        pipeline_head="Knowledge pipeline",
        pipeline=["Ingest", "Parse", "Chunk", "Embed", "Index", "Enrich"],
        pipeline_note="Summaries, questions, graph and tags run stage by stage on the async task queue",
        platform=[("shield", "Workspaces", "RBAC · audit log"), ("model", "Model catalog", "27 vendors"),
                  ("server", "Task queue", "Worker pools"), ("trace", "Observability", "Langfuse tracing")],
        services=[("file", "Document parsing", "docreader · gRPC"), ("sandbox", "Session sandbox", "Docker · E2B · Cube"),
                  ("model", "Model providers", "27 vendors · Ollama"), ("search", "Web search", "Bing · SearXNG …"),
                  ("plug", "External MCP", "Tools · OAuth2"), ("sources", "Data sources", "Feishu · Confluence …")],
        stores=[("PostgreSQL", "Data · BM25 · pgvector"), ("Redis", "Queue · streams · limits"),
                ("Vector store", "ES · Qdrant · Milvus … (optional)"), ("Object storage", "Local · MinIO · COS · S3 …"),
                ("Neo4j", "Knowledge graph (optional)"), ("Langfuse", "Tracing (optional)")],
    ),
    "ja": dict(
        heads=["クライアントとチャネル", "WeKnora アプリ", "ランタイムサービス", "ストレージ"],
        access=[("monitor", "Web UI", "Vue 3 · HTTP / SSE"), ("terminal", "API · CLI · SDK", "スコープ付き API キー"),
                ("channels", "IM チャネル", "Feishu · Slack など"), ("code", "ウェブ埋め込み", "Widget · ドメイン許可"),
                ("plug", "MCP クライアント", "Streamable HTTP"), ("browser", "ローカルブラウザ", "BrowserSkill · WebSocket")],
        modes=[("search", "RAG Q&A", ["クエリ理解", "ハイブリッド検索", "リランク", "引用付き回答"]),
               ("agent", "Agent 推論", ["ReAct 推論", "ツール · スキル", "サンドボックス実行", "長期メモリ"]),
               ("wiki", "自動 Wiki", ["ページ生成", "ナレッジグラフ", "手動編集", "ロールバック"])],
        shared="3 つの機能は同じナレッジベースを共有",
        pipeline_head="ナレッジ処理パイプライン",
        pipeline=["取り込み", "解析", "チャンク", "埋め込み", "索引", "拡充"],
        pipeline_note="要約・質問・グラフ・タグは非同期タスクキューで段階的に処理",
        platform=[("shield", "ワークスペース", "RBAC · 監査ログ"), ("model", "モデルカタログ", "27 ベンダー"),
                  ("server", "タスクキュー", "Worker プール"), ("trace", "可観測性", "Langfuse トレース")],
        services=[("file", "文書解析", "docreader · gRPC"), ("sandbox", "サンドボックス", "Docker · E2B · Cube"),
                  ("model", "モデル", "27 ベンダー · Ollama"), ("search", "Web 検索", "Bing · SearXNG など"),
                  ("plug", "外部 MCP", "ツール · OAuth2"), ("sources", "データソース", "Feishu · Confluence など")],
        stores=[("PostgreSQL", "業務データ · BM25 · pgvector"), ("Redis", "タスクキュー · ストリーム"),
                ("ベクトル DB", "ES · Qdrant · Milvus など（任意）"), ("オブジェクト ストレージ", "ローカル · MinIO · COS · S3 など"),
                ("Neo4j", "ナレッジグラフ（任意）"), ("Langfuse", "トレーシング（任意）")],
    ),
    "ko": dict(
        heads=["클라이언트와 채널", "WeKnora 앱", "런타임 서비스", "스토리지"],
        access=[("monitor", "Web UI", "Vue 3 · HTTP / SSE"), ("terminal", "API · CLI · SDK", "범위 지정 API 키"),
                ("channels", "IM 채널", "Feishu · Slack 등"), ("code", "웹사이트 임베드", "Widget · 도메인 허용"),
                ("plug", "MCP 클라이언트", "Streamable HTTP"), ("browser", "로컬 브라우저", "BrowserSkill · WebSocket")],
        modes=[("search", "RAG Q&A", ["질의 이해", "하이브리드 검색", "재순위", "인용 포함 답변"]),
               ("agent", "Agent 추론", ["ReAct 추론", "도구 · 스킬 · MCP", "샌드박스 실행", "장기 메모리"]),
               ("wiki", "자동 Wiki", ["페이지 생성", "지식 그래프", "수동 편집", "버전 롤백"])],
        shared="세 기능은 같은 지식베이스를 공유",
        pipeline_head="지식 처리 파이프라인",
        pipeline=["가져오기", "파싱", "청킹", "임베딩", "인덱싱", "보강"],
        pipeline_note="요약·질문·그래프·태그는 비동기 작업 큐에서 단계별로 처리",
        platform=[("shield", "워크스페이스", "RBAC · 감사 로그"), ("model", "모델 카탈로그", "27개 벤더"),
                  ("server", "작업 큐", "Worker 풀"), ("trace", "관측 가능성", "Langfuse 추적")],
        services=[("file", "문서 파싱", "docreader · gRPC"), ("sandbox", "샌드박스", "Docker · E2B · Cube"),
                  ("model", "모델", "27개 벤더 · Ollama"), ("search", "웹 검색", "Bing · SearXNG 등"),
                  ("plug", "외부 MCP", "도구 · OAuth2"), ("sources", "데이터 소스", "Feishu · Confluence 등")],
        stores=[("PostgreSQL", "업무 데이터 · BM25 · pgvector"), ("Redis", "작업 큐 · 스트림"),
                ("벡터 DB", "ES · Qdrant · Milvus 등(선택)"), ("오브젝트 스토리지", "로컬 · MinIO · COS · S3 등"),
                ("Neo4j", "지식 그래프(선택)"), ("Langfuse", "추적(선택)")],
    ),
}
EYEBROWS = ["01 / ACCESS", "02 / WEKNORA APP", "03 / SERVICES", "04 / STORAGE"]


def wrap_sep(s, size, width, sep=" · "):
    """Wrap at " · " separators first; fall back to wrap() for a segment that is too long on its own."""
    lines, cur = [], ""
    for seg in s.split(sep):
        cand = seg if not cur else cur + sep + seg
        if cur and text_width(cand, size) > width:
            lines.append(cur + sep.rstrip())
            cur = seg
        else:
            cur = cand
    lines.append(cur)
    out = []
    for line in lines:
        out.extend(wrap(line, size, width) if text_width(line, size) > width else [line])
    return out


def fit(label, text, size, width):
    if text_width(text, size) > width:
        print(f"  warn: {label!r} is {text_width(text, size):.0f}px, wider than {width}px")


def architecture(lang, theme):
    c, t = ARCH[lang], THEMES[theme]
    S, M = sans(lang), MONO
    W = 1600
    ax, aw = 40, 340            # access column
    cx, cw = 420, 760           # core panel
    sx, sw = 1220, 340          # services column
    top = 40
    item_h, item_gap, items_y = 88, 12, top + 118
    col_h = 118 + 6 * item_h + 5 * item_gap + 26
    o = []

    def panel(x, y, w, h, eyebrow, head, meta=None):
        o.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="18" fill="{t["panel"]}" stroke="{t["rule"]}" stroke-width="2"/>')
        o.append(f'<text x="{x+24}" y="{y+46}" font-family="{M}" font-size="17" letter-spacing="2" fill="{t["gold"]}">{eyebrow}</text>')
        o.append(f'<text x="{x+24}" y="{y+84}" font-family="{S}" font-size="26" font-weight="600" fill="{t["ink"]}">{esc(head)}</text>')
        if meta:
            o.append(f'<text x="{x+w-24}" y="{y+46}" text-anchor="end" font-family="{M}" font-size="16" letter-spacing="2" fill="{t["mute"]}">{meta}</text>')

    def item_list(x, w, rows):
        centers = []
        for i, (ic, name, sub) in enumerate(rows):
            y = items_y + i * (item_h + item_gap)
            fit(name, name, 22, w - 40 - 80 - 12)
            fit(sub, sub, 17, w - 40 - 80 - 12)
            o.extend([
                f'<rect x="{x+20}" y="{y}" width="{w-40}" height="{item_h}" rx="12" fill="{t["card"]}" stroke="{t["rule"]}" stroke-width="2"/>',
                f'<rect x="{x+36}" y="{y+20}" width="48" height="48" rx="10" fill="{t["icon_bg"]}"/>',
                icon(ic, x + 46, y + 30, 28, t["gold"], 1.6),
                f'<text x="{x+100}" y="{y+39}" font-family="{S}" font-size="22" font-weight="600" fill="{t["ink"]}">{esc(name)}</text>',
                f'<text x="{x+100}" y="{y+67}" font-family="{S}" font-size="17" fill="{t["soft"]}">{esc(sub)}</text>',
            ])
            centers.append(y + item_h / 2)
        return centers

    def arrowhead(x, y, direction):
        d = {"right": f"M{x-9} {y-6}L{x} {y}L{x-9} {y+6}", "down": f"M{x-6} {y-9}L{x} {y}L{x+6} {y-9}",
             "up": f"M{x-6} {y+9}L{x} {y}L{x+6} {y+9}"}[direction]
        return f'<path d="{d}" fill="none" stroke="{t["gold"]}" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>'

    line = lambda d: f'<path d="{d}" fill="none" stroke="{t["gold"]}" stroke-width="2" stroke-linecap="round" opacity=".85"/>'
    dot = lambda x, y: f'<circle cx="{x}" cy="{y}" r="4" fill="{t["gold"]}"/>'

    # 01 access and 03 services
    panel(ax, top, aw, col_h, EYEBROWS[0], c["heads"][0])
    a_centers = item_list(ax, aw, c["access"])
    panel(sx, top, sw, col_h, EYEBROWS[2], c["heads"][2])
    s_centers = item_list(sx, sw, c["services"])

    # 02 core panel
    panel(cx, top, cw, col_h, EYEBROWS[1], c["heads"][1], meta="GO · GIN · ASYNQ")
    pad, gap = 20, 16
    mw = (cw - 2 * pad - 2 * gap) / 3
    my = top + 118
    mode_lines = [[l2 for l in lines for l2 in wrap_sep(l, 20, mw - 40)] for _, _, lines in c["modes"]]
    mh = 132 + max(len(ls) for ls in mode_lines) * 30
    mode_centers = []
    for i, ((ic, title, _), lines) in enumerate(zip(c["modes"], mode_lines)):
        x = cx + pad + i * (mw + gap)
        fit(title, title, 24, mw - 40)
        o.extend([
            f'<rect x="{x}" y="{my}" width="{mw}" height="{mh}" rx="14" fill="{t["card"]}" stroke="{t["rule"]}" stroke-width="2"/>',
            f'<rect x="{x+20}" y="{my+20}" width="48" height="48" rx="10" fill="{t["icon_bg"]}"/>',
            icon(ic, x + 30, my + 30, 28, t["gold"], 1.6),
            f'<text x="{x+20}" y="{my+104}" font-family="{S}" font-size="24" font-weight="600" fill="{t["ink"]}">{esc(title)}</text>',
        ])
        for j, l in enumerate(lines):
            o.append(f'<text x="{x+20}" y="{my+140+j*30}" font-family="{S}" font-size="20" fill="{t["soft"]}">{esc(l)}</text>')
        mode_centers.append(x + mw / 2)

    # knowledge pipeline, feeding all three modes
    py = my + mh + 64
    px, pw = cx + pad, cw - 2 * pad
    ph = 158
    o.append(f'<rect x="{px}" y="{py}" width="{pw}" height="{ph}" rx="14" fill="{t["chip_bg"]}" stroke="{t["chip"]}" stroke-width="2"/>')
    for mx in mode_centers:
        o.append(line(f"M{mx} {py}V{my+mh+4}"))
        o.append(arrowhead(mx, my + mh + 4, "up"))
    shared_w = text_width(c["shared"], 18) + 36
    o.extend([
        f'<rect x="{cx+cw/2-shared_w/2}" y="{my+mh+16}" width="{shared_w}" height="32" rx="16" fill="{t["panel"]}" stroke="{t["chip"]}" stroke-width="1.5"/>',
        f'<text x="{cx+cw/2}" y="{my+mh+38}" text-anchor="middle" font-family="{S}" font-size="18" fill="{t["accent"]}">{esc(c["shared"])}</text>',
        f'<text x="{px+20}" y="{py+38}" font-family="{S}" font-size="20" font-weight="600" fill="{t["ink"]}">{esc(c["pipeline_head"])}</text>',
    ])
    n, cg = len(c["pipeline"]), 26
    chw = (pw - 40 - (n - 1) * cg) / n
    for i, step in enumerate(c["pipeline"]):
        x = px + 20 + i * (chw + cg)
        fit(step, step, 20, chw - 12)
        o.extend([
            f'<rect x="{x}" y="{py+56}" width="{chw}" height="44" rx="9" fill="{t["card"]}" stroke="{t["chip"]}" stroke-width="1.5"/>',
            f'<text x="{x+chw/2}" y="{py+85}" text-anchor="middle" font-family="{S}" font-size="20" fill="{t["ink"]}">{esc(step)}</text>',
        ])
        if i < n - 1:
            o.append(icon("chevron", x + chw + cg / 2 - 9, py + 69, 18, t["gold"], 2))
    fit("note", c["pipeline_note"], 17, pw - 40)
    o.append(f'<text x="{px+20}" y="{py+136}" font-family="{S}" font-size="17" fill="{t["mute"]}">{esc(c["pipeline_note"])}</text>')

    # platform capabilities, 2 x 2
    ly = py + ph + 16
    lg = 12
    lh = (top + col_h - 20 - ly - lg) / 2
    lw = (pw - lg) / 2
    for i, (ic, name, sub) in enumerate(c["platform"]):
        x = px + (i % 2) * (lw + lg)
        y = ly + (i // 2) * (lh + lg)
        nw = text_width(name, 20)
        fit(name + sub, name + "  " + sub, 19, lw - 70)
        o.extend([
            f'<rect x="{x}" y="{y}" width="{lw}" height="{lh}" rx="12" fill="{t["card"]}" stroke="{t["rule"]}" stroke-width="2"/>',
            icon(ic, x + 18, y + lh / 2 - 13, 26, t["gold"], 1.6),
            f'<text x="{x+58}" y="{y+lh/2+7}" font-family="{S}" font-size="20" font-weight="600" fill="{t["ink"]}">{esc(name)}</text>',
            f'<text x="{x+58+nw+14}" y="{y+lh/2+7}" font-family="{S}" font-size="17" fill="{t["soft"]}">{esc(sub)}</text>',
        ])

    # connectors: access -> core, core -> services
    bus_a = ax + aw + 18
    for y in a_centers:
        o.append(line(f"M{ax+aw-20} {y}H{bus_a}"))
    o.append(line(f"M{bus_a} {a_centers[0]}V{a_centers[-1]}"))
    mid = my + mh / 2
    o.append(line(f"M{bus_a} {mid}H{cx-3}"))
    o.extend([arrowhead(cx - 3, mid, "right"), dot(bus_a, mid)])
    bus_s = sx - 18
    o.append(line(f"M{cx+cw+3} {mid}H{bus_s}"))
    o.append(line(f"M{bus_s} {s_centers[0]}V{s_centers[-1]}"))
    o.append(dot(bus_s, mid))
    for y in s_centers:
        o.append(line(f"M{bus_s} {y}H{sx+17}"))
        o.append(arrowhead(sx + 17, y, "right"))

    # 04 storage
    gy = top + col_h + 50
    tx0, tg = ax + 230, 14
    tw = (W - ax - 20 - tx0 - 5 * tg) / 6
    tiles = [(wrap_sep(n, 21, tw - 60, sep=" "), wrap_sep(sub, 17, tw - 36)) for n, sub in c["stores"]]
    tile_h = max(38 + len(nl) * 26 + 8 + (len(sl) - 1) * 24 + 26 for nl, sl in tiles)
    gh = max(176, tile_h + 40)
    o.append(f'<rect x="{ax}" y="{gy}" width="{W-2*ax}" height="{gh}" rx="18" fill="{t["panel"]}" stroke="{t["rule"]}" stroke-width="2"/>')
    o.append(f'<text x="{ax+24}" y="{gy+gh/2-10}" font-family="{M}" font-size="17" letter-spacing="2" fill="{t["gold"]}">{EYEBROWS[3]}</text>')
    o.append(f'<text x="{ax+24}" y="{gy+gh/2+28}" font-family="{S}" font-size="26" font-weight="600" fill="{t["ink"]}">{esc(c["heads"][3])}</text>')
    for i, (name_lines, sub_lines) in enumerate(tiles):
        x = tx0 + i * (tw + tg)
        ty = gy + 20
        o.extend([
            f'<rect x="{x}" y="{ty}" width="{tw}" height="{gh-40}" rx="12" fill="{t["card"]}" stroke="{t["rule"]}" stroke-width="2"/>',
            icon("db", x + 18, ty + 18, 24, t["gold"], 1.6),
        ])
        for j, l in enumerate(name_lines):
            o.append(f'<text x="{x+52}" y="{ty+38+j*26}" font-family="{S}" font-size="21" font-weight="600" fill="{t["ink"]}">{esc(l)}</text>')
        base = ty + 38 + len(name_lines) * 26 + 8
        for j, l in enumerate(sub_lines):
            o.append(f'<text x="{x+18}" y="{base+j*24}" font-family="{S}" font-size="17" fill="{t["soft"]}">{esc(l)}</text>')
    # core -> storage
    x0 = cx + cw / 2
    o.append(line(f"M{x0} {top+col_h+3}V{gy+17}"))
    o.append(arrowhead(x0, gy + 17, "down"))

    H = gy + gh + 40
    head = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" role="img" aria-label="WeKnora architecture">'
            f'<rect width="{W}" height="{H}" fill="none"/>')
    return head + "\n" + "\n".join(o) + "\n</svg>"


for lang in ARCH:
    for theme in THEMES:
        print(f"architecture {lang} {theme}")
        open(f"{OUT}/architecture-{lang}-{theme}.svg", "w").write(architecture(lang, theme))


for lang in COPY:
    for theme in THEMES:
        open(f"{OUT}/hero-{lang}-{theme}.svg", "w").write(hero(lang, theme))
        open(f"{OUT}/capabilities-{lang}-{theme}.svg", "w").write(cards(lang, theme))
print("ok")

# Small gold line icons for README tables; the gold reads on both GitHub themes.
TABLE_ICONS = {
    "terminal": '<rect x="2" y="3" width="20" height="18" rx="2"/><path d="m6 8 4 4-4 4m7 0h5"/>',
    "plug": '<path d="M9 2v5m6-5v5M6 7h12v4a6 6 0 0 1-12 0V7Zm6 10v5"/>',
    "phone": '<rect x="6" y="2" width="12" height="20" rx="2.5"/><path d="M11 18h2"/>',
    "skills": '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><path d="M14 17.5h7m-3.5-3.5v7"/>',
    "code": '<path d="m7 7-5 5 5 5m10-10 5 5-5 5m-3-14-4 18"/>',
    "braces": '<path d="M8 3H7a2 2 0 0 0-2 2v4.5a2.5 2.5 0 0 1-2.5 2.5A2.5 2.5 0 0 1 5 14.5V19a2 2 0 0 0 2 2h1m8-18h1a2 2 0 0 1 2 2v4.5a2.5 2.5 0 0 0 2.5 2.5 2.5 2.5 0 0 0-2.5 2.5V19a2 2 0 0 1-2 2h-1"/>',
    "server": ICONS["server"],
}
os.makedirs(f"{OUT}/icons", exist_ok=True)
for name, body in TABLE_ICONS.items():
    open(f"{OUT}/icons/{name}.svg", "w").write(
        f'<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#b8863b" '
        f'stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">{body}</svg>\n')
