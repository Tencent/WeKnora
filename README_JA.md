<p align="center">
  <picture>
    <img src="./docs/images/logo.png" alt="WeKnora Logo" height="120"/>
  </picture>
</p>
<p align="center">
  <picture>
    <a href="https://trendshift.io/repositories/15289" target="_blank">
      <img src="https://trendshift.io/api/badge/repositories/15289" alt="Tencent%2FWeKnora | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/>
    </a>
  </picture>
</p>

<p align="center">
    <a href="https://weknora.weixin.qq.com" target="_blank">
        <img alt="公式サイト" src="https://img.shields.io/badge/公式サイト-WeKnora-4e6b99">
    </a>
    <a href="https://chatbot.weixin.qq.com" target="_blank">
        <img alt="WeChat対話オープンプラットフォーム" src="https://img.shields.io/badge/WeChat対話オープンプラットフォーム-5ac725">
    </a>
    <a href="https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd" target="_blank">
        <img alt="Chrome 拡張機能" src="https://img.shields.io/badge/Chrome 拡張機能-WeKnora-4285F4">
    </a>
    <a href="https://clawhub.ai/lyingbug/weknora" target="_blank">
        <img alt="ClawHub Skill" src="https://img.shields.io/badge/ClawHub Skill-WeKnora-ff6b35">
    </a>
    <a href="https://github.com/Tencent/WeKnora/blob/main/LICENSE">
        <img src="https://img.shields.io/badge/License-MIT-ffffff?labelColor=d4eaf7&color=2e6cc4" alt="License">
    </a>
    <a href="./CHANGELOG.md">
        <img alt="バージョン" src="https://img.shields.io/badge/version-0.6.3-2e6cc4?labelColor=d4eaf7">
    </a>
</p>

<p align="center">
| <a href="./README.md"><b>English</b></a> | <a href="./README_CN.md"><b>简体中文</b></a> | <b>日本語</b> | <a href="./README_KO.md"><b>한국어</b></a> |
</p>

<p align="center">
  <h4 align="center">

  [プロジェクト紹介](#-プロジェクト紹介) • [アーキテクチャ設計](#️-アーキテクチャ設計) • [コア機能](#-コア機能) • [クイックスタート](#-クイックスタート) • [ドキュメント](#-ドキュメント) • [開発ガイド](#-開発ガイド)

  </h4>
</p>

# 💡 WeKnora — ドキュメントを「生きたナレッジ」へ：RAG・Agent 推論・自動 Wiki を統合した LLM ナレッジフレームワーク

## 📌 プロジェクト紹介

[**WeKnora（ウィーノラ）**](https://weknora.weixin.qq.com) は、大規模言語モデル（LLM）をベースとしたオープンソースのナレッジフレームワークで、エンタープライズ級の文書理解、セマンティック検索、自律推論シナリオ向けに設計されています。

本フレームワークは **3 つのコア能力** を中心に構築されています：日常的な検索に最適な **RAG ベースのクイック Q&A**、ナレッジ検索・MCP ツール・Web 検索を自律的にオーケストレーションし複雑なマルチステップタスクを処理する **ReAct Agent 推論**、そして Agent が生のドキュメントから相互リンクされた Markdown ナレッジベースとインタラクティブなナレッジグラフを自律生成・維持する全く新しい **Wiki モード**。さらに、多様なデータソース連携（Feishu / Notion / Yuque / RSS、随時拡充中）、**ウェブサイト埋め込み Widget** による外部サイトへのエージェント公開、20 以上の LLM プロバイダー統合、Langfuse による全体可観測性、**エンタープライズ向けマルチテナント RBAC（4 階層ロールマトリクス + リソース所有権 + テナント監査ログ）**、完全セルフホスト可能なモジュラーアーキテクチャと組み合わせることで、WeKnora は散在する文書を「検索可能・推論可能・継続的に進化する」専用ナレッジ資産へと昇華させます。

Feishu、Notion、Yuqueなどの外部プラットフォームからのナレッジ自動同期（他のデータソースも順次対応中）に対応し、PDF、Word、画像、Excelなど10以上の文書フォーマットをサポート。WeChat Work、Feishu、Slack、TelegramなどのIMチャネルから直接Q&Aサービスを提供できます。モデル層ではOpenAI、DeepSeek、Qwen（Alibaba Cloud）、Zhipu、Hunyuan、Gemini、MiniMax、NVIDIA、Ollamaなど主要プロバイダーに対応。全プロセスをモジュラー設計し、大規模モデル、ベクトルデータベース、ストレージなどのコンポーネントを柔軟に差し替え可能。ローカルおよびプライベートクラウドデプロイに対応し、データは完全に自己管理可能です。さらにWeKnoraは **Langfuse** とシームレスに統合され、Agentの推論、トークン消費、パイプラインに対する包括的な可観測性（オブザーバビリティ）を提供します。

## ✨ 最新アップデート

- **v0.6.3** — ウェブサイト埋め込み Widget と統合センター（セキュアモード Token 交換 + レート制限）；チャット体験の全面刷新（引用ポップオーバー、RAG パイプライン進捗、ストリーミング Markdown）；ドキュメント複数タグと一括 reparse；Wiki フォルダーと階層ナビゲーション；RSS データソース；MCP OAuth2；EPUB / MHTML 解析；Agent モデル準備状態チェック；モデルデバッガー；セッションソースフィルター；ワークスペース削除 UI。詳細は [`CHANGELOG.md`](./CHANGELOG.md)。
- **v0.6.2** — アップロード単位の解析設定（`process_config`）+ アップロード確認ダイアログ；reparse 時の設定上書き；`weknora` CLI v0.9（同梱 Agent Skills、`session stop`、auth/profile 統合）；KB マーキー複数選択；pgvector 1024 次元 HNSW インデックス；チャットリソース Store 刷新；Langfuse のみのトレーシング（Jaeger 削除）。詳細は [`CHANGELOG.md`](./CHANGELOG.md)。
- **v0.6.1** — ドキュメント解析トレースタイムライン（Langfuse 風の Span ツリー、ステージごとの進捗表示 + 解析中止）；OpenSearch ベクター DB ドライバー；YAML 宣言型ビルトインモデル設定；システム管理者と統合プラットフォーム設定 + 監査ログ；新規ユーザーオンボーディングガイド；設定 UI 刷新；`weknora` CLI v0.7 / v0.8（Agent ファースト ワイヤープロトコル、NDJSON、`--dry-run`）；OpenDataLoader と PaddleOCR-VL 解析エンジン；MCP サーバーのマルチトランスポート（stdio / SSE / HTTP）；モデル単位の思考モード設定；Tencent LKEAP リランク + ネイティブ Gemini Embedding + MiniMax-M3。詳細は [`CHANGELOG.md`](./CHANGELOG.md) を参照。
- **v0.6.0** — テナント RBAC（4 階層ロールマトリクス `Owner` / `Admin` / `Contributor` / `Viewer` + KB 単位の所有 + テナントごとの監査ログ）、テナントメンバー管理とマルチワークスペース UX、セルフサービスでのワークスペース作成；`weknora` CLI v0.4 GA + `mcp serve`；KB 検索の複数ベクター DB ファンアウト；MCP / データソース資格情報の AES-256-GCM 暗号化 + docreader gRPC TLS + Token；Zhipu Embedder と華為雲 OBS の追加；サーバーサイドユーザー設定；Go 1.26.0。詳細は [`docs/RBAC说明.md`](./docs/RBAC说明.md) と [`CHANGELOG.md`](./CHANGELOG.md) を参照。
- **v0.5.2** — Wiki インジェストが万件規模 KB に対応（タスクキュー + DLQ）；MCP 工具人機審批；Anthropic / Apache Doris / Tencent VectorDB / 金山雲 KS3 / SearXNG バックエンド；適応型 3 段階チャンキング + ライブプレビュー；グローバル ⌘K コマンドパレット；Yuque コネクタ + WeChat ミニプログラム；`weknora` CLI プレビュー版。
- **v0.5.1** — KB 一括管理；テナント全体の IM チャネル概観；セッション検索 + ユーザー単位ピン留め；モデル / Web 検索 / MCP 統一カード設定；Agent ごとの LLM タイムアウト；デスクトップ版テナント切替。
- **v0.5.0** — Wiki モード GA — Agent が原文書から構造化・相互リンクされた Markdown Wiki ページとナレッジグラフを自動生成、Wiki ブラウザと可視化グラフを UI に搭載。
- **v0.4.0** — WeKnora Cloud（ホスティング LLM + 解析）；Chrome 拡張機能；ClawHub Skill；WeChat IM；添付ファイル処理；Azure OpenAI / Alibaba OSS；Notion コネクタ；Baidu + Ollama Web 検索；VectorStore 管理。
- **v0.3.6** — ASR（音声）；Feishu データソース自動同期；OIDC；IM 引用返信 + スレッドベースセッション；ドキュメント自動要約；Tavily 検索；並列ツール呼び出し；Agent @メンション範囲制限。
- **v0.3.5** — Telegram / DingTalk / Mattermost IM；IM スラッシュコマンド + QA キュー；推奨質問；VLM による MCP ツール画像自動説明；Novita AI；チャネルトラッキング。
- **v0.3.4** — 企業 WeChat / Feishu / Slack IM；マルチモーダル画像；NVIDIA モデル API；Weaviate；AWS S3；AES-256-GCM API キー暗号化；組み込み MCP サービス；ハイブリッド検索最適化；`final_answer` ツール。
- **v0.3.3** — 親子チャンキング；KB ピン留め；フォールバック応答；Rerank パッセージクリーニング；ストレージバケット自動作成；Milvus。
- **v0.3.2** — ナレッジ検索エントリ；ソース別パーサー / ストレージエンジン設定；ローカルストレージ画像レンダリング；ドキュメントプレビュー；Volcengine TOS；Mermaid レンダリング；対話バッチ管理；メモリグラフプレビュー。
- **v0.3.0** — 共有スペース；Agent Skills + サンドボックス実行；カスタム Agent；データ分析 Agent；思考モード；Bing / Google 検索；API Key 認証；Helm Chart；韓国語 i18n；Qdrant。
- **v0.2.0** — Agent モード（ReACT）；複数タイプのナレッジベース（FAQ + ドキュメント）；対話戦略設定；DuckDuckGo Web 検索；MCP ツール統合；新 UI + Agent モード切替；MQ 非同期タスク管理。


## 📱 機能デモ

<table>
  <tr>
    <td colspan="2" align="center"><b>💬 インテリジェント Q&A 対話</b><br/><img src="./docs/images/qa.png" alt="インテリジェント Q&A 対話" width="100%"></td>
  </tr>
  <tr>
    <td width="50%" align="center"><b>📖 Wiki ブラウザ</b><br/><img src="./docs/images/wiki-browser.png" alt="Wiki ブラウザ" width="100%"></td>
    <td width="50%" align="center"><b>🕸️ Wiki ナレッジグラフ</b><br/><img src="./docs/images/wiki-graph.png" alt="Wiki ナレッジグラフ" width="100%"></td>
  </tr>
  <tr>
    <td width="50%" align="center"><b>🤖 Agent モード · ツール呼び出しプロセス</b><br/><img src="./docs/images/agent-qa.png" alt="Agent モードツール呼び出しプロセス" width="100%"></td>
    <td width="50%" align="center"><b>⚙️ 対話設定</b><br/><img src="./docs/images/settings.png" alt="対話設定" width="100%"></td>
  </tr>
  <tr>
    <td colspan="2" align="center"><b>🔭 可観測性 · Langfuse Tracing</b><br/><img src="./docs/images/langfuse.png" alt="Langfuse Tracing" width="100%"></td>
  </tr>
</table>

## 🏗️ アーキテクチャ設計

![weknora-architecture.png](./docs/images/architecture.png)

文書解析・ベクトル化・検索から大規模モデル推論まで、全パイプラインをモジュラー分離。各コンポーネントは柔軟に差し替え・拡張可能。ローカル / プライベートクラウドデプロイに対応し、データ完全自己管理、ゼロバリアの Web UI で即座に利用開始。


## 🧩 機能概要

**インテリジェント対話**

| 機能 | 詳細 |
|------|------|
| インテリジェント推論 | ReACT プログレッシブ・マルチステップ推論、ナレッジ検索・MCP ツール・Web 検索を自律的にオーケストレーション |
| クイック Q&A | ナレッジベースベースの RAG Q&A、迅速かつ正確な回答 |
| Wiki モード | Agent主導で生のドキュメントから構造化された相互リンク済みMarkdown Wikiページを自動生成・保守 |
| ツール呼び出し | 組み込みツール、MCP ツール（OAuth2 リモートサービス含む）、Web 検索 |
| 対話戦略 | オンライン Prompt 編集、検索閾値チューニング、マルチターン文脈認識 |
| 推奨質問 | ナレッジベースの内容に基づく質問の自動生成 |
| 引用と RAG 進捗 | インライン引用ポップオーバー、統一 Markdown レンダリング、RAG パイプラインの段階別進捗表示 |
| セッション管理 | サイドバーでソース別（Web / IM / 埋め込み）にセッションをフィルター・グループ化 |

**ナレッジ管理**

| 機能 | 詳細 |
|------|------|
| ナレッジベースタイプ | FAQ / ドキュメント / Wiki、フォルダーインポート・URL インポート・複数タグ管理・オンライン入力 |
| アップロード単位の解析設定 | アップロード確認ダイアログまたは `process_config` API でパーサー・チャンキング・マルチモーダル（VLM / ASR）・グラフ抽出・質問生成をバッチ単位で上書き；reparse 時も設定変更可能 |
| 一括 reparse | 複数ドキュメントの解析を一度に再キュー、バッチ単位の `process_config` 対応 |
| データソースインポート | Feishu / Notion / Yuque / RSS フィードの自動同期（他のデータソースも開発中）、増分・全量同期対応 |
| 文書フォーマット | PDF / Word / Txt / Markdown / HTML / EPUB / MHTML / 画像 / CSV / Excel / PPT / JSON |
| 検索戦略 | BM25 疎検索 / Dense 密検索 / GraphRAG グラフ強化 / 親子チャンキング / pgvector HNSW 加速（1024 次元）/ 多次元インデックス |
| 一括選択 | KB リストでマーキー（ドラッグ）複数選択によるバッチ操作 |
| E2E テスト | 検索+生成の全パイプライン可視化、リコール的中率・BLEU / ROUGE 指標評価 |

**連携と拡張**

| 機能 | 詳細 |
|------|------|
| 大規模モデル | OpenAI / Azure OpenAI / Anthropic (Claude) / DeepSeek / Qwen (Alibaba Cloud) / Zhipu / Hunyuan / Doubao (Volcengine) / Gemini / MiniMax / NVIDIA / Novita AI / SiliconFlow / OpenRouter / Ollama |
| Embedding | Ollama / BGE / GTE / OpenAI 互換 API |
| ベクトル DB | PostgreSQL (pgvector) / Elasticsearch / OpenSearch / Milvus / Weaviate / Qdrant / Apache Doris / Tencent VectorDB |
| オブジェクトストレージ | ローカル / MinIO / AWS S3 / 火山引擎 TOS / Alibaba Cloud OSS / 金山雲 KS3 |
| IM 統合 | WeChat Work / Feishu / Slack / Telegram / DingTalk / Mattermost / WeChat |
| ウェブ埋め込み | 埋め込み Widget でエージェントを公開、ドメイン許可リスト・レート制限・セキュアモード Token 交換 |
| Web 検索 | DuckDuckGo / Bing / Google / Tavily / Baidu / Ollama / SearXNG |

**プラットフォーム**

| 機能 | 詳細 |
|------|------|
| デプロイ | ローカル / Docker / Kubernetes (Helm)、プライベート化・オフラインデプロイ対応 |
| UI | Web UI / RESTful API / CLI (`weknora`) / Chrome Extension / ウェブ埋め込み Widget / WeChat ミニプログラム |
| 可観測性 | Langfuse（唯一のトレーシングバックエンド）で ReAct ループ・トークン消費・ツール呼び出し・パイプライン追跡；Langfuse 風のドキュメント解析トレースタイムラインを内蔵し、ステージごとの進捗を表示 |
| タスク管理 | MQ 非同期タスク、バージョンアップ時の DB 自動マイグレーション |
| モデル管理 | 集中設定、YAML 宣言型ビルトインモデル設定、ナレッジベース単位のモデル選択、モデル単位の思考モード・Embedding 次元上書き、インタラクティブモデルデバッガー、マルチテナント組み込みモデル共有、WeKnora Cloud ホスティングモデルとドキュメント解析 |

## 🧩 Chrome 拡張機能

[**WeKnora Chrome 拡張機能**](https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd)を使えば、ブラウザからWebコンテンツをWeKnoraナレッジベースに直接取り込めます。テキスト、画像、ページ全体を選択してワンクリックでナレッジエントリとして保存——コピペやファイルアップロード不要です。

## 🦞 ClawHub Skill

[**WeKnora ClawHub Skill**](https://clawhub.ai/lyingbug/weknora)はClawHubプラットフォームで公開されたWeKnoraスキルです。インストール後、WeKnora REST APIを通じてドキュメントのアップロード（ファイル / URL / Markdown）、ハイブリッド検索（ベクトル + キーワード）、ナレッジエントリの管理が可能になります。

- **ドキュメントインポート** — エージェント経由でファイルアップロード、Webページインポート、Markdownナレッジの作成
- **ハイブリッド検索** — 単一または複数のナレッジベースをベクトル + キーワードで横断検索
- **ナレッジ管理** — プログラムによるナレッジエントリの閲覧、編集、削除


## 🚀 クイックスタート

### 🛠 環境要件

- [Docker](https://www.docker.com/) & [Docker Compose](https://docs.docker.com/compose/)
- [Git](https://git-scm.com/)

### 📦 インストール・起動

```bash
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora
cp .env.example .env   # 必要に応じて .env を編集（詳細はファイル内のコメント参照）
docker compose up -d   # コアサービスを起動
```

起動後、**http://localhost** にアクセスして利用開始。

> ローカル Ollama モデルを使用する場合は、先に `ollama serve > /dev/null 2>&1 &` を実行してください。

### 🔧 オプションサービス（Docker Compose Profile）

`--profile` フラグで追加コンポーネントを有効化。複数の profile を組み合わせ可能：

| Profile | 説明 | コマンド |
|---------|------|---------|
| _(デフォルト)_ | コアサービス | `docker compose up -d` |
| `full` | 全機能 | `docker compose --profile full up -d` |
| `neo4j` | ナレッジグラフ (Neo4j) | `docker compose --profile neo4j up -d` |
| `minio` | オブジェクトストレージ (MinIO) | `docker compose --profile minio up -d` |
| `langfuse` | トレーシング (Langfuse) | `docker compose --profile langfuse up -d` |

組み合わせ例：`docker compose --profile neo4j --profile minio up -d`

サービス停止：`docker compose down`

### 🌐 サービスアドレス

| サービス | URL |
|---------|-----|
| Web UI | `http://localhost` |
| バックエンド API | `http://localhost:8080` |
| Langfuse トレーシング | `http://localhost:3000` |

## 文書ナレッジグラフ

WeKnoraは文書をナレッジグラフに変換し、文書内の異なる段落間の関連関係を表示することをサポートします。ナレッジグラフ機能を有効にすると、システムは文書内部の意味関連ネットワークを分析・構築し、ユーザーが文書内容を理解するのを助けるだけでなく、インデックスと検索に構造化サポートを提供し、検索結果の関連性と幅を向上させます。

詳細な設定については、[ナレッジグラフ設定ガイド](./docs/KnowledgeGraph.md)をご参照ください。

## 対応するMCPサーバー  

[MCP設定ガイド](./mcp-server/MCP_CONFIG.md) をご参照のうえ、必要な設定を行ってください。


## 🔌 WeChat対話オープンプラットフォームの使用

WeKnoraは[WeChat対話オープンプラットフォーム](https://chatbot.weixin.qq.com)のコア技術フレームワークとして、より簡単な使用方法を提供します：

- **ノーコードデプロイメント**：知識をアップロードするだけで、WeChatエコシステムで迅速にインテリジェントQ&Aサービスをデプロイし、「即座に質問して即座に回答」の体験を実現
- **効率的な問題管理**：高頻度の問題の独立した分類管理をサポートし、豊富なデータツールを提供して、正確で信頼性が高く、メンテナンスが容易な回答を保証
- **WeChatエコシステムカバレッジ**：WeChat対話オープンプラットフォームを通じて、WeKnoraのインテリジェントQ&A能力を公式アカウント、ミニプログラムなどのWeChatシナリオにシームレスに統合し、ユーザーインタラクション体験を向上


## 📘 ドキュメント

よくある問題の解決：[よくある問題](./docs/QA.md)

詳細なAPIドキュメントは：[APIドキュメント](./docs/api/README.md)を参照してください

製品計画と今後の機能：[Roadmap](./docs/ROADMAP.md)

## 🧭 開発ガイド

### ⚡ 高速開発モード（推奨）

コードを頻繁に変更する必要がある場合、**Dockerイメージを毎回再構築する必要はありません**！高速開発モードを使用してください：

```bash
# インフラストラクチャを起動
make dev-start

# バックエンドを起動（新しいターミナル）
make dev-app

# フロントエンドを起動（新しいターミナル）
make dev-frontend
```

**開発の利点：**
- ✅ フロントエンドの変更は自動ホットリロード（再起動不要）
- ✅ バックエンドの変更は高速再起動（5-10秒、Airホットリロードをサポート）
- ✅ Dockerイメージを再構築する必要がない
- ✅ IDEブレークポイントデバッグをサポート

**詳細ドキュメント：** [開発環境クイックスタート](./docs/开发指南.md)

## 🤝 貢献ガイド

[Issue](https://github.com/Tencent/WeKnora/issues) や Pull Request の提出を歓迎します。

**フロー：** Fork → ブランチ作成 → 変更をコミット → PR を作成

**規約：** `gofmt` でコードをフォーマット、[Conventional Commits](https://www.conventionalcommits.org/) に従う（`feat:` / `fix:` / `docs:` / `test:` / `refactor:`）

## 🔒 セキュリティ通知

**重要：** v0.1.3バージョンより、WeKnoraにはシステムセキュリティを強化するためのログイン認証機能が含まれています。v0.2.0では、さらに多くの機能強化と改善が追加されました。本番環境でのデプロイメントにおいて、以下を強く推奨します：

- WeKnoraサービスはパブリックインターネットではなく、内部/プライベートネットワーク環境にデプロイしてください
- 重要な情報漏洩を防ぐため、サービスを直接パブリックネットワークに公開することは避けてください
- デプロイメント環境に適切なファイアウォールルールとアクセス制御を設定してください
- セキュリティパッチと改善のため、定期的に最新バージョンに更新してください

## 👥 コントリビューター

素晴らしいコントリビューターに感謝します：

[![Contributors](https://contrib.rocks/image?repo=Tencent/WeKnora)](https://github.com/Tencent/WeKnora/graphs/contributors)

## 📄 ライセンス

このプロジェクトは[MIT](./LICENSE)ライセンスの下で公開されています。
このプロジェクトのコードを自由に使用、変更、配布できますが、元の著作権表示を保持する必要があります。

## 📈 プロジェクト統計

<a href="https://www.star-history.com/#Tencent/WeKnora&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&legend=top-left" />
 </picture>
</a>
