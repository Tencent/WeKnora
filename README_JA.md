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
    <a href="https://github.com/Tencent/WeKnora/blob/main/LICENSE">
        <img src="https://img.shields.io/badge/License-MIT-ffffff?labelColor=d4eaf7&color=2e6cc4" alt="License">
    </a>
    <a href="./CHANGELOG.md">
        <img alt="バージョン" src="https://img.shields.io/badge/version-0.3.6-2e6cc4?labelColor=d4eaf7">
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

# 💡 WeKnora - 大規模言語モデルベースの文書理解検索フレームワーク

## 📌 プロジェクト紹介

[**WeKnora（ウィーノラ）**](https://weknora.weixin.qq.com) は、大規模言語モデル（LLM）をベースとしたインテリジェントなナレッジ管理・Q&Aフレームワークで、エンタープライズ向けの文書理解と意味検索シナリオに特化して設計されています。

WeKnora は**クイック Q&A** と**インテリジェント推論**の 2 つの Q&A モードを提供します。クイック Q&A は **RAG（Retrieval-Augmented Generation）** パイプラインで関連フラグメントを素早く検索・回答を生成し、日常的なナレッジ検索に最適です。インテリジェント推論は **ReACT Agent** エンジンにより、**プログレッシブ戦略**でナレッジ検索、MCP ツール、Web 検索を自律的にオーケストレーションし、反復推論とリフレクションで段階的に結論を導きます。複数情報源の統合や複雑なタスクに最適です。カスタムエージェントにも対応し、専用のナレッジベース、ツールセット、システムプロンプトを柔軟に設定できます。用途に応じてモードを選択し、応答速度と推論の深さを両立します。

Feishuなどの外部プラットフォームからのナレッジ自動同期（他のデータソースも順次対応中）に対応し、PDF、Word、画像、Excelなど10以上の文書フォーマットをサポート。WeChat Work、Feishu、Slack、TelegramなどのIMチャネルから直接Q&Aサービスを提供できます。モデル層ではOpenAI、DeepSeek、Qwen（Alibaba Cloud）、Zhipu、Hunyuan、Gemini、MiniMax、NVIDIA、Ollamaなど主要プロバイダーに対応。全プロセスをモジュラー設計し、大規模モデル、ベクトルデータベース、ストレージなどのコンポーネントを柔軟に差し替え可能。ローカルおよびプライベートクラウドデプロイに対応し、データは完全に自己管理可能です。

**公式サイト：** https://weknora.weixin.qq.com

## ✨ 最新アップデート

**v0.3.6 バージョンのハイライト:**

- **ASR（自動音声認識）**：ASRモデルを統合し、音声ファイルのアップロード、ドキュメント内音声プレビュー、音声文字起こし機能をサポート
- **データソース自動同期（Feishu）**：完全なデータソース管理機能、Feishu Wiki/ドライブの自動同期（増分/全量）、同期ログ、テナント分離
- **OIDC認証**：OpenID Connectログインをサポート、自動ディスカバリ、カスタムエンドポイント設定、ユーザー情報マッピング
- **IM引用返信コンテキスト**：IMチャネルで引用メッセージを抽出してLLMプロンプトに注入し、文脈に基づく回答を実現。非テキスト引用の幻覚防止処理
- **IMスレッドベースセッション**：IMチャネル（Slack、Mattermost、Feishu、Telegram）でスレッド単位のセッションモードをサポート、スレッド内でのマルチユーザーコラボレーション
- **ドキュメント自動要約**：AI生成のドキュメント要約、入力サイズの設定が可能、ドキュメント詳細画面に専用の要約セクション
- **Tavily Web検索**：Tavilyを新しいWeb検索プロバイダーとして追加、Web検索プロバイダーアーキテクチャを拡張性向上のためリファクタリング
- **MCP自動再接続**：サーバー接続断絶時のMCPツール呼び出しの自動再接続ロジック
- **並列ツール呼び出し**：Agentモードでerrgroupを使用して複数のツール呼び出しを並行実行、複雑なタスク処理を高速化
- **Agent @メンション範囲制限**：ユーザーの@メンションをAgentが許可されたナレッジベースの範囲内に制限、不正アクセスを防止
- **ログインページパフォーマンス**：backdrop-filter blurをすべて削除、アニメーション要素を削減、GPUコンポジティングヒントを追加

**v0.3.5 バージョンのハイライト:**

- **Telegram、DingTalk & Mattermost IM統合**：Telegramボット（webhook/ロングポーリング、editMessageTextストリーミング）、DingTalkボット（webhook/Streamモード、AIカードストリーミング）、Mattermost アダプターを新規追加。IMチャネルはWeChat Work、Feishu、Slack、Telegram、DingTalk、Mattermost の6プラットフォームをカバー
- **IMスラッシュコマンドとQAキュー**：プラグイン式スラッシュコマンドフレームワーク（/help、/info、/search、/stop、/clear）、有界QAワーカープール、ユーザー単位レート制限、RedisベースのマルチインスタンスDistributed Coordination
- **推奨質問**：Agentが関連ナレッジベースに基づいてコンテキスト対応の推奨質問を自動生成し、チャットインターフェースに表示。画像ナレッジは質問生成タスクを自動キュー登録
- **VLMによるMCPツール画像自動説明**：MCPツールが画像を返した場合、設定されたVLMモデルを使用してテキスト説明を自動生成し、テキストのみのLLMでも画像内容を利用可能に
- **Novita AIプロバイダー**：OpenAI互換APIでchat、embedding、VLLMモデルタイプをサポートする新しいLLMプロバイダー
- **MCPツール名の安定性**：ツール名をUUIDではなくservice.Nameから生成（再接続後も安定）。衝突防止制約を追加。フロントエンドでsnake_caseを人間が読みやすい形式に整形
- **チャネルトラッキング**：ナレッジエントリとメッセージにchannelフィールド追加（web/api/im/browser_extension）
- **重要バグ修正**：ナレッジベース未設定時のAgent空レスポンス、中国語/絵文字ドキュメントのUTF-8切り詰め、テナント設定更新時のAPIキー暗号化消失、vLLMストリーミング推論コンテンツ欠落、Rerankの空パッセージエラーを修正

<details>
<summary><b>過去のリリース</b></summary>

**v0.3.4 バージョンのハイライト:**

- **IMボット統合**：企業WeChat、Feishu、SlackのIMチャネルをサポート、WebSocket/Webhookモード、ストリーミング対応、ナレッジベース統合
- **マルチモーダル画像サポート**：画像アップロードとマルチモーダル画像処理、セッション管理の強化
- **手動ナレッジダウンロード**：手動ナレッジコンテンツのファイルダウンロード、ファイル名サニタイズ対応
- **NVIDIA モデルAPI**：NVIDIAチャットモデルAPIをサポート、カスタムエンドポイントとVLMモデル設定
- **Weaviateベクトルデータベース**：ナレッジ検索用にWeaviateベクトルデータベースバックエンドを追加
- **AWS S3ストレージ**：AWS S3ストレージアダプターを統合、設定UIとデータベースマイグレーション
- **AES-256-GCM暗号化**：APIキーをAES-256-GCMで静的暗号化、セキュリティ強化
- **組み込みMCPサービス**：組み込みMCPサービスサポートでAgent機能を拡張
- **ハイブリッド検索最適化**：ターゲットのグループ化とクエリ埋め込みの再利用で検索性能を向上
- **Final Answerツール**：新しいfinal_answerツールとAgentの所要時間追跡でワークフローを改善

**v0.3.3 バージョンのハイライト:**

- **親子チャンキング**：階層型の親子チャンキング戦略により、コンテキスト管理と検索精度を強化
- **ナレッジベースのピン留め**：よく使うナレッジベースをピン留めして素早くアクセス
- **フォールバックレスポンス**：関連する結果がない場合のフォールバックレスポンス処理とUIインジケーター
- **Rerankパッセージクリーニング**：Rerankモデルのパッセージクリーニング機能で関連性スコアの精度を向上
- **バケット自動作成**：ストレージエンジン接続チェックの強化、バケットの自動作成をサポート
- **Milvusベクトルデータベース**：ナレッジ検索用にMilvusベクトルデータベースバックエンドを追加

**v0.3.2 バージョンのハイライト:**

- 🔍 **ナレッジ検索**：新しい「ナレッジ検索」エントリポイント、セマンティック検索をサポートし、検索結果を直接会話ウィンドウに持ち込み可能
- ⚙️ **パーサーとストレージエンジンの設定**：設定画面でソースごとのドキュメントパーサーとストレージエンジンを設定可能、ナレッジベースでファイルタイプ別のパーサー選択をサポート
- 🖼️ **ローカルストレージ画像レンダリング**：ローカルストレージモードで会話中の画像レンダリングをサポート、ストリーミング中の画像プレースホルダーを最適化
- 📄 **ドキュメントプレビュー**：ユーザーがアップロードした元のファイルをプレビューする組み込みドキュメントプレビューコンポーネント
- 🎨 **UI最適化**：ナレッジベース、エージェント、共有スペースリストページのインタラクションを再設計
- 🗄️ **Milvusサポート**：ナレッジ検索用にMilvusベクトルデータベースバックエンドを追加
- 🌋 **Volcengine TOS**：Volcengine TOSオブジェクトストレージサポートを追加
- 📊 **Mermaidレンダリング**：チャットでMermaidダイアグラムのレンダリングをサポート、フルスクリーンビューアー、ズーム、パン、ツールバー、エクスポート機能付き
- 💬 **バッチ会話管理**：バッチ管理と全セッション一括削除機能
- 🔗 **リモートURLナレッジ**：リモートファイルURLからナレッジエントリの作成をサポート
- 🧠 **メモリグラフプレビュー**：ユーザーレベルのメモリグラフ可視化プレビュー
- 🔄 **非同期再解析**：既存のナレッジドキュメントの非同期再処理API

**v0.3.0 バージョンのハイライト:**

- 🏢 **共有スペース**：共有スペース管理、メンバー招待、メンバー間でのナレッジベースとAgentの共有、テナント分離検索
- 🧩 **Agentスキル**：Agentスキルシステム、スマート推論向けプリロードスキル、サンドボックスベースのセキュリティ分離実行環境
- 🤖 **カスタムAgent**：カスタムAgentの作成・設定・選択をサポート、ナレッジベース選択モード（全部/指定/無効）
- 📊 **データアナリストAgent**：組み込みデータアナリストAgent、CSV/Excel分析用DataSchemaツール
- 🧠 **思考モード**：LLMとAgentの思考モードをサポート、思考コンテンツのインテリジェントフィルタリング
- 🔍 **検索エンジン拡張**：DuckDuckGoに加えてBingとGoogleの検索プロバイダーを追加
- 📋 **FAQ強化**：バッチインポートドライラン、類似質問、検索結果のマッチ質問フィールド、大量インポートのオブジェクトストレージオフロード
- 🔑 **API Key認証**：API Key認証メカニズム、Swaggerドキュメントセキュリティ設定
- 📎 **入力内選択**：入力ボックスでナレッジベースとファイルを直接選択、@メンション表示
- ☸️ **Helm Chart**：Kubernetesデプロイメント用の完全なHelm Chart、Neo4j GraphRAGサポート
- 🌍 **国際化**：韓国語（한국어）サポートを追加
- 🔒 **セキュリティ強化**：SSRF安全HTTPクライアント、強化されたSQLバリデーション、MCP stdio転送セキュリティ、サンドボックスベース実行
- ⚡ **インフラストラクチャ**：Qdrantベクトルデータベースサポート、Redis ACL、設定可能なログレベル、Ollama埋め込み最適化、`DISABLE_REGISTRATION`制御

**v0.2.0 バージョンのハイライト：**

- 🤖 **Agentモード**：新規ReACT Agentモードを追加、組み込みツール、MCPツール、Web検索を呼び出し、複数回の反復とリフレクションを通じて包括的なサマリーレポートを提供
- 📚 **複数タイプのナレッジベース**：FAQとドキュメントの2種類のナレッジベースをサポート、フォルダーインポート、URLインポート、タグ管理、オンライン入力機能を新規追加
- ⚙️ **対話戦略**：Agentモデル、通常モードモデル、検索閾値、Promptの設定をサポート、マルチターン対話の動作を精密に制御
- 🌐 **Web検索**：拡張可能なWeb検索エンジンをサポート、DuckDuckGo検索エンジンを組み込み
- 🔌 **MCPツール統合**：MCPを通じてAgent機能を拡張、uvx、npx起動ツールを組み込み、複数の転送方式をサポート
- 🎨 **新UI**：対話インターフェースを最適化、Agentモード/通常モードの切り替え、ツール呼び出しプロセスの表示、ナレッジベース管理インターフェースの全面的なアップグレード
- ⚡ **インフラストラクチャのアップグレード**：MQ非同期タスク管理を導入、データベース自動マイグレーションをサポート、高速開発モードを提供

</details>


## 🏗️ アーキテクチャ設計

![weknora-architecture.png](./docs/images/architecture.png)

文書解析・ベクトル化・検索から大規模モデル推論まで、全パイプラインをモジュラー分離。各コンポーネントは柔軟に差し替え・拡張可能。ローカル / プライベートクラウドデプロイに対応し、データ完全自己管理、ゼロバリアの Web UI で即座に利用開始。


## 🧩 機能概要

**🤖 インテリジェント対話**

| 機能 | 詳細 |
|------|------|
| インテリジェント推論 | ReACT プログレッシブ・マルチステップ推論、ナレッジ検索・MCP ツール・Web 検索を自律的にオーケストレーション、カスタムエージェント対応 |
| クイック Q&A | ナレッジベースベースの RAG Q&A、迅速かつ正確な回答 |
| ツール呼び出し | 組み込みツール、MCP ツール、Web 検索 |
| 対話戦略 | オンライン Prompt 編集、検索閾値チューニング、マルチターン文脈認識 |
| 推奨質問 | ナレッジベースの内容に基づく質問の自動生成 |

**📚 ナレッジ管理**

| 機能 | 詳細 |
|------|------|
| ナレッジベースタイプ | FAQ / ドキュメント、フォルダーインポート・URL インポート・タグ管理・オンライン入力 |
| データソースインポート | Feishuナレッジベースの自動同期（他のデータソースも開発中）、増分・全量同期対応 |
| 文書フォーマット | PDF / Word / Txt / Markdown / HTML / 画像 / CSV / Excel / PPT / JSON |
| 検索戦略 | BM25 疎検索 / Dense 密検索 / GraphRAG グラフ強化 / 親子チャンキング / 多次元インデックス |
| E2E テスト | 検索+生成の全パイプライン可視化、リコール的中率・BLEU / ROUGE 指標評価 |

**🔌 連携と拡張**

| 機能 | 詳細 |
|------|------|
| 大規模モデル | OpenAI / DeepSeek / Qwen (Alibaba Cloud) / Zhipu / Hunyuan / Doubao (Volcengine) / Gemini / MiniMax / NVIDIA / Novita AI / SiliconFlow / OpenRouter / Ollama |
| Embedding | Ollama / BGE / GTE / OpenAI 互換 API |
| ベクトル DB | PostgreSQL (pgvector) / Elasticsearch / Milvus / Weaviate / Qdrant |
| オブジェクトストレージ | ローカル / MinIO / AWS S3 / 火山引擎 TOS |
| IM 統合 | WeChat Work / Feishu / Slack / Telegram / DingTalk / Mattermost |
| Web 検索 | DuckDuckGo / Bing / Google / Tavily |

**🛡️ プラットフォーム**

| 機能 | 詳細 |
|------|------|
| デプロイ | ローカル / Docker / Kubernetes (Helm)、プライベート化・オフラインデプロイ対応 |
| UI | Web UI / RESTful API / Chrome Extension |
| タスク管理 | MQ 非同期タスク、バージョンアップ時の DB 自動マイグレーション |
| モデル管理 | 集中設定、ナレッジベース単位のモデル選択、マルチテナント組み込みモデル共有 |

## 🚀 クイックスタート

### 🛠 環境要件

以下のツールがローカルにインストールされていることを確認してください：

* [Docker](https://www.docker.com/)
* [Docker Compose](https://docs.docker.com/compose/)
* [Git](https://git-scm.com/)

### 📦 インストール手順

#### ① コードリポジトリのクローン

```bash
# メインリポジトリをクローン
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora
```

#### ② 環境変数の設定

```bash
# サンプル設定ファイルをコピー
cp .env.example .env

# .envを編集し、対応する設定情報を入力
# すべての変数の説明は.env.exampleのコメントを参照
```

#### ③ メインサービスを起動します


#### Ollama を個別に起動する (オプション)

`.env` でローカル Ollama モデルを設定している場合は、追加で Ollama サービスを起動してください。

```bash
ollama serve > /dev/null 2>&1 &
```

#### さまざまな機能の組み合わせを有効にする

- 最小限のコアサービス
```bash
docker compose up -d
```

- すべての機能を有効にする
```bash
docker compose --profile full up -d
```

- トレースログが必要
```bash
docker compose --profile jaeger up -d
```

- Neo4j ナレッジグラフが必要
```bash
docker compose --profile neo4j up -d
```

- Minio ファイルストレージサービスが必要
```bash
docker compose --profile minio up -d
```

- 複数のオプションの組み合わせ
```bash
docker compose --profile neo4j --profile minio up -d
```

#### ④ サービスの停止

```bash
docker compose down
```

### 🌐 サービスアクセスアドレス

起動成功後、以下のアドレスにアクセスできます：

* Web UI：`http://localhost`
* バックエンドAPI：`http://localhost:8080`
* リンクトレース（Jaeger）：`http://localhost:16686`

## 📱 機能デモ

<table>
  <tr>
    <td colspan="2"><b>インテリジェントQ&A対話</b><br/><img src="./docs/images/qa.png" alt="インテリジェントQ&A対話"></td>
  </tr>
  <tr>
    <td colspan="2"><b>Agentモードツール呼び出しプロセス</b><br/><img src="./docs/images/agent-qa.png" alt="Agentモードツール呼び出しプロセス"></td>
  </tr>
    <tr>
    <td><b>ナレッジベース管理</b><br/><img src="./docs/images/knowledgebases.png" alt="ナレッジベース管理"></td>
    <td><b>対話設定</b><br/><img src="./docs/images/settings.png" alt="対話設定"></td>
  </tr>
</table>

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

### 📁 プロジェクトディレクトリ構造

```
WeKnora/  
├── client/      # Goクライアント  
├── cmd/         # アプリケーションエントリ  
├── config/      # 設定ファイル  
├── docker/      # Dockerイメージファイル  
├── docreader/   # 文書解析プロジェクト  
├── docs/        # プロジェクトドキュメント  
├── frontend/    # フロントエンドプロジェクト  
├── internal/    # コアビジネスロジック  
├── mcp-server/  # MCPサーバー  
├── migrations/  # データベースマイグレーションスクリプト  
└── scripts/     # 起動およびツールスクリプト
```

## 🤝 貢献ガイド

コミュニティユーザーの貢献を歓迎します！提案、バグ、新機能のリクエストがある場合は、[Issue](https://github.com/Tencent/WeKnora/issues)を通じて提出するか、直接Pull Requestを提出してください。

### 🎯 貢献方法

- 🐛 **バグ修正**: システムの欠陥を発見して修正
- ✨ **新機能**: 新しい機能を提案して実装
- 📚 **ドキュメント改善**: プロジェクトドキュメントを改善
- 🧪 **テストケース**: ユニットテストと統合テストを作成
- 🎨 **UI/UX最適化**: ユーザーインターフェースと体験を改善

### 📋 貢献フロー

1. **プロジェクトをFork** してあなたのGitHubアカウントへ
2. **機能ブランチを作成** `git checkout -b feature/amazing-feature`
3. **変更をコミット** `git commit -m 'Add amazing feature'`
4. **ブランチをプッシュ** `git push origin feature/amazing-feature`
5. **Pull Requestを作成** して変更内容を詳しく説明

### 🎨 コード規約

- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)に従う
- `gofmt`を使用してコードをフォーマット
- 必要なユニットテストを追加
- 関連ドキュメントを更新

### 📝 コミット規約

[Conventional Commits](https://www.conventionalcommits.org/)規約を使用：

```
feat: 文書バッチアップロード機能を追加
fix: ベクトル検索精度の問題を修正
docs: APIドキュメントを更新
test: 検索エンジンテストケースを追加
refactor: 文書解析モジュールをリファクタリング
```

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
