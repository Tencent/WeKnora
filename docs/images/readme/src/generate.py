"""Generate the README artwork in the product website's visual language.

Outputs hero-{cn,en}-{light,dark}.svg, capabilities-{cn,en}-{light,dark}.svg and
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
    tokens, buf = [], ""
    for ch in s:
        if breaks_anywhere(ch):
            if buf:
                tokens.append(buf); buf = ""
            tokens.append(ch)
        elif ch == " ":
            tokens.append(buf + " "); buf = ""
        else:
            buf += ch
    if buf:
        tokens.append(buf)
    lines, cur = [], ""
    for t in tokens:
        if cur and text_width((cur + t).rstrip(), size) > width and t not in "，。、；：）」":
            lines.append(cur.rstrip()); cur = t.lstrip()
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
        modes=[("search", "RAG", "问答"), ("agent", "Agent", "推理"), ("wiki", "自动", "Wiki")],
        trust=[("github", "腾讯开源 · MIT License"), ("server", "支持私有化部署"), ("model", "自由选择模型与存储")],
        cards=[
            ("search", "01 / RAG", "回答有据可查", "结合语义与关键词检索查找相关资料，回答附带来源引用，可打开原文核对。", ["混合检索", "多模态解析", "原文引用"]),
            ("agent", "02 / AGENT", "用知识和工具完成任务", "智能体根据任务检索知识库、搜索网页、调用 MCP 工具与技能，在沙箱中处理文件、运行脚本，并可跨会话记住你确认过的偏好。", ["多步推理", "工具调用", "技能执行", "长期记忆"]),
            ("wiki", "03 / WIKI", "把文档整理成 Wiki", "从原始文档生成相互链接的 Wiki 页面与知识图谱，支持浏览、编辑和版本回滚。", ["自动组织", "知识图谱", "版本回滚"]),
        ],
    ),
    "en": dict(
        eyebrow="TENCENT OPEN SOURCE · WEKNORA",
        h1=["Find the answers,", "and put knowledge to work."], h1_size=80,
        desc=["Tencent's open-source knowledge management framework.", "Bring team documents together for Q&A, tasks and wikis."],
        modes=[("search", "RAG", "Q&A"), ("agent", "Agent", "reasoning"), ("wiki", "Auto", "Wiki")],
        trust=[("github", "Tencent Open Source · MIT License"), ("server", "Self-hosted deployment"), ("model", "Your choice of models and storage")],
        cards=[
            ("search", "01 / RAG", "Answers you can check", "Semantic and keyword search find the relevant material. Every answer cites its sources, and you can open the original to verify.", ["Hybrid search", "Multimodal parsing", "Citations"]),
            ("agent", "02 / AGENT", "Tasks done with knowledge and tools", "The agent searches knowledge bases and the web, calls MCP tools and skills, handles files and scripts in a sandbox, and remembers preferences you have confirmed.", ["Multi-step", "Tool calling", "Skills", "Memory"]),
            ("wiki", "03 / WIKI", "Documents organized into a wiki", "Builds interlinked wiki pages and a knowledge graph from raw documents, with browsing, editing and version rollback.", ["Auto-organized", "Knowledge graph", "Rollback"]),
        ],
    ),
    "ja": dict(
        eyebrow="TENCENT OPEN SOURCE · WEKNORA",
        h1=["答えを見つけ、", "知識を実践に活かす。"], h1_size=88,
        desc=["Tencent が公開するオープンソースのエンタープライズ向けナレッジ管理フレームワーク。", "チームの資料を集約し、Q&A・タスク実行・Wiki 整理に活用します。"],
        modes=[("search", "RAG", "Q&A"), ("agent", "Agent", "推論"), ("wiki", "自動", "Wiki")],
        trust=[("github", "Tencent オープンソース · MIT License"), ("server", "プライベートデプロイ対応"), ("model", "モデルとストレージを自由に選択")],
        cards=[
            ("search", "01 / RAG", "根拠のある回答", "セマンティック検索とキーワード検索を組み合わせて関連資料を探し、回答には出典が付きます。原文を開いて確認できます。", ["ハイブリッド検索", "マルチモーダル解析", "原文引用"]),
            ("agent", "02 / AGENT", "知識とツールでタスクを完了", "エージェントがタスクに応じてナレッジベースや Web を検索し、MCP ツールやスキルを呼び出し、サンドボックスでファイル処理やスクリプト実行を行います。確認済みの好みはセッションをまたいで記憶します。", ["マルチステップ推論", "ツール呼び出し", "スキル実行", "長期メモリ"]),
            ("wiki", "03 / WIKI", "ドキュメントを Wiki に整理", "原文書から相互リンクされた Wiki ページとナレッジグラフを生成し、閲覧・編集・バージョンのロールバックに対応します。", ["自動整理", "ナレッジグラフ", "ロールバック"]),
        ],
    ),
    "ko": dict(
        eyebrow="TENCENT OPEN SOURCE · WEKNORA",
        h1=["답을 찾고,", "지식을 실천으로."], h1_size=88,
        desc=["Tencent가 오픈소스로 공개한 엔터프라이즈 지식 관리 프레임워크입니다.", "팀 자료를 모아 Q&A, 작업 실행, Wiki 정리에 활용합니다."],
        modes=[("search", "RAG", "Q&A"), ("agent", "Agent", "추론"), ("wiki", "자동", "Wiki")],
        trust=[("github", "Tencent 오픈소스 · MIT License"), ("server", "프라이빗 배포 지원"), ("model", "모델과 스토리지 자유 선택")],
        cards=[
            ("search", "01 / RAG", "근거 있는 답변", "시맨틱 검색과 키워드 검색으로 관련 자료를 찾고, 답변에 출처를 표시합니다. 원문을 열어 확인할 수 있습니다.", ["하이브리드 검색", "멀티모달 파싱", "원문 인용"]),
            ("agent", "02 / AGENT", "지식과 도구로 작업 완료", "에이전트가 작업에 맞춰 지식베이스와 웹을 검색하고 MCP 도구와 스킬을 호출하며, 샌드박스에서 파일을 처리하고 스크립트를 실행합니다. 확인한 선호는 세션을 넘어 기억합니다.", ["다단계 추론", "도구 호출", "스킬 실행", "장기 메모리"]),
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
    # three modes, stacked on the right, joined by a dashed rule
    mx, my = 1216, 178
    o.append(f'<path d="M{mx+36} {my+72}V{my+72+2*104}" stroke="{t["chip"]}" stroke-width="2" stroke-dasharray="4 6"/>')
    for i, (ic, a, b) in enumerate(c["modes"]):
        y = my + i * 104
        o += [f'<rect x="{mx}" y="{y}" width="312" height="76" rx="14" fill="{t["card"]}" stroke="{t["rule"]}" stroke-width="2"/>',
              f'<rect x="{mx+14}" y="{y+14}" width="48" height="48" rx="10" fill="{t["icon_bg"]}"/>',
              icon(ic, mx + 24, y + 24, 28, t["gold"], 1.6),
              f'<text x="{mx+82}" y="{y+48}" font-family="{sans(lang)}" font-size="25" font-weight="600" fill="{t["ink"]}">{esc(a)} <tspan font-weight="400" fill="{t["soft"]}">{esc(b)}</tspan></text>']
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
