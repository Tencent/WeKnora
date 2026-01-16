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
        <img alt="バージョン" src="https://img.shields.io/badge/version-0.2.10-2e6cc4?labelColor=d4eaf7">
    </a>
</p>

<p align="center">
| <a href="./README.md"><b>English</b></a> | <a href="./README_CN.md"><b>简体中文</b></a> | <b>日本語</b> |
</p>

<p align="center">
  <h4 align="center">

  [プロジェクト紹介](#-プロジェクト紹介) • [アーキテクチャ設計](#️-アーキテクチャ設計) • [コア機能](#-コア機能) • [クイックスタート](#-クイックスタート) • [ドキュメント](#-ドキュメント) • [開発ガイド](#-開発ガイド)

  </h4>
</p>

# 💡 WeKnora - 大規模言語モデルベースの文書理解検索フレームワーク

## 📌 プロジェクト紹介

[**WeKnora（ウィーノラ）**](https://weknora.weixin.qq.com) は、大規模言語モデル（LLM）をベースとした文書理解と意味検索フレームワークで、構造が複雑で内容が異質な文書シナリオ向けに特別に設計されています。

フレームワークはモジュラーアーキテクチャを採用し、マルチモーダル前処理、意味ベクトルインデックス、インテリジェント検索、大規模モデル生成推論を統合して、効率的で制御可能な文書Q&Aワークフローを構築します。コア検索プロセスは **RAG（Retrieval-Augmented Generation）** メカニズムに基づいており、文脈関連フラグメントと言語モデルを組み合わせて、より高品質な意味的回答を実現します。

**公式サイト：** https://weknora.weixin.qq.com

## ✨ 最新アップデート

**v0.2.0 バージョンのハイライト：**

- 🤖 **Agentモード**：新規ReACT Agentモードを追加、組み込みツール、MCPツール、Web検索を呼び出し、複数回の反復とリフレクションを通じて包括的なサマリーレポートを提供
- 📚 **複数タイプのナレッジベース**：FAQとドキュメントの2種類のナレッジベースをサポート、フォルダーインポート、URLインポート、タグ管理、オンライン入力機能を新規追加
- ⚙️ **対話戦略**：Agentモデル、通常モードモデル、検索閾値、Promptの設定をサポート、マルチターン対話の動作を精密に制御
- 🌐 **Web検索**：拡張可能なWeb検索エンジンをサポート、DuckDuckGo検索エンジンを組み込み
- 🔌 **MCPツール統合**：MCPを通じてAgent機能を拡張、uvx、npx起動ツールを組み込み、複数の転送方式をサポート
- 🎨 **新UI**：対話インターフェースを最適化、Agentモード/通常モードの切り替え、ツール呼び出しプロセスの表示、ナレッジベース管理インターフェースの全面的なアップグレード
- ⚡ **インフラストラクチャのアップグレード**：MQ非同期タスク管理を導入、データベース自動マイグレーションをサポート、高速開発モードを提供

## 🔒 セキュリティ通知

**重要：** v0.1.3バージョンより、WeKnoraにはシステムセキュリティを強化するためのログイン認証機能が含まれています。v0.2.0では、さらに多くの機能強化と改善が追加されました。本番環境でのデプロイメントにおいて、以下を強く推奨します：

- WeKnoraサービスはパブリックインターネットではなく、内部/プライベートネットワーク環境にデプロイしてください
- 重要な情報漏洩を防ぐため、サービスを直接パブリックネットワークに公開することは避けてください
- デプロイメント環境に適切なファイアウォールルールとアクセス制御を設定してください
- セキュリティパッチと改善のため、定期的に最新バージョンに更新してください

## 🏗️ アーキテクチャ設計

![weknora-pipelone.png](./docs/images/architecture.png)

WeKnoraは現代的なモジュラー設計を採用し、完全な文書理解と検索パイプラインを構築しています。システムには主に文書解析、ベクトル化処理、検索エンジン、大規模モデル推論などのコアモジュールが含まれ、各コンポーネントは柔軟に設定および拡張できます。

## 🎯 コア機能

- **🤖 Agentモード**：ReACT Agentモードをサポート、組み込みツールでナレッジベースを検索、MCPツールとWeb検索を呼び出し、複数回の反復とリフレクションを通じて包括的なサマリーレポートを提供
- **🔍 正確な理解**：PDF、Word、画像などの文書の構造化コンテンツ抽出をサポートし、統一された意味ビューを構築
- **🧠 インテリジェント推論**：大規模言語モデルを活用して文書コンテキストとユーザーの意図を理解し、正確なQ&Aとマルチターン対話をサポート
- **📚 複数タイプのナレッジベース**：FAQとドキュメントの2種類のナレッジベースをサポート、フォルダーインポート、URLインポート、タグ管理、オンライン入力機能
- **🔧 柔軟な拡張**：解析、埋め込み、検索から生成までの全プロセスを分離し、柔軟な統合とカスタマイズ拡張を容易に
- **⚡ 効率的な検索**：複数の検索戦略のハイブリッド：キーワード、ベクトル、ナレッジグラフ、クロスナレッジベース検索をサポート
- **🌐 Web検索**：拡張可能なWeb検索エンジンをサポート、DuckDuckGo検索エンジンを組み込み
- **🔌 MCPツール統合**：MCPを通じてAgent機能を拡張、uvx、npx起動ツールを組み込み、複数の転送方式をサポート
- **⚙️ 対話戦略**：Agentモデル、通常モードモデル、検索閾値、Promptの設定をサポート、マルチターン対話の動作を精密に制御
- **🎯 使いやすさ**：直感的なWebインターフェースと標準API、技術的な障壁なしで素早く開始可能
- **🔒 セキュアで制御可能**：ローカルおよびプライベートクラウドデプロイメントをサポート、データは完全に自己管理可能

## 📊 適用シナリオ

| 応用シナリオ | 具体的な応用 | コア価値 |
|---------|----------|----------|
| **企業ナレッジ管理** | 内部文書検索、規則Q&A、操作マニュアル照会 | ナレッジ検索効率の向上、トレーニングコストの削減 |
| **科学研究文献分析** | 論文検索、研究レポート分析、学術資料整理 | 文献調査の加速、研究意思決定の支援 |
| **製品技術サポート** | 製品マニュアルQ&A、技術文書検索、トラブルシューティング | カスタマーサービス品質の向上、技術サポート負担の軽減 |
| **法的コンプライアンス審査** | 契約条項検索、法規政策照会、ケース分析 | コンプライアンス効率の向上、法的リスクの削減 |
| **医療知識支援** | 医学文献検索、診療ガイドライン照会、症例分析 | 臨床意思決定の支援、診療品質の向上 |

## 🧩 機能モジュール能力

| 機能モジュール | サポート状況                                              | 説明 |
|---------|-----------------------------------------------------|------|
| Agentモード | ✅ ReACT Agentモード                                    | 組み込みツールでナレッジベースを検索、MCPツールとWeb検索を使用、クロスナレッジベース検索、複数回の反復とリフレクションをサポート |
| ナレッジベースタイプ | ✅ FAQ / ドキュメント                                      | FAQとドキュメントの2種類のナレッジベースの作成をサポート、フォルダーインポート、URLインポート、タグ管理、オンライン入力機能 |
| 文書フォーマットサポート | ✅ PDF / Word / Txt / Markdown / 画像（OCR / Caption含む） | 様々な構造化・非構造化文書コンテンツの解析をサポート、図文混在と画像文字抽出をサポート |
| モデル管理 | ✅ 集中設定、組み込みモデル共有                                    | モデルの集中設定、ナレッジベース設定ページにモデル選択を追加、マルチテナント間での組み込みモデル共有をサポート |
| 埋め込みモデルサポート | ✅ ローカルモデル、BGE / GTE API等                            | カスタムembeddingモデルをサポート、ローカルデプロイとクラウドベクトル生成インターフェースに対応 |
| ベクトルデータベース接続 | ✅ PostgreSQL（pgvector）、Elasticsearch                | 主流のベクトルインデックスバックエンドをサポート、柔軟な切り替えと拡張が可能、異なる検索シナリオに適応 |
| 検索メカニズム | ✅ BM25 / Dense Retrieve / GraphRAG                  | 密・疎検索、ナレッジグラフ強化検索など複数の戦略をサポート、検索-再ランキング-生成プロセスを自由に組み合わせ可能 |
| 大規模モデル統合 | ✅ Qwen、DeepSeek等をサポート、思考/非思考モード切り替え                 | ローカル大規模モデル（Ollama起動など）に接続可能、または外部APIサービスを呼び出し、推論モードの柔軟な設定をサポート |
| 対話戦略 | ✅ Agentモデル、通常モードモデル、検索閾値、Prompt設定                   | Agentモデル、通常モードに必要なモデル、検索閾値の設定をサポート、オンラインPrompt設定、マルチターン対話の動作を精密に制御 |
| Web検索 | ✅ 拡張可能な検索エンジン、DuckDuckGo / Google                   | 拡張可能なWeb検索エンジンをサポート、DuckDuckGo検索エンジンを組み込み |
| MCPツール | ✅ uvx、npx起動ツール、Stdio/HTTP Streamable/SSE            | MCPを通じてAgent機能を拡張、uvx、npxの2種類のMCP起動ツールを組み込み、3種類の転送方式をサポート |
| Q&A能力 | ✅ コンテキスト認識、マルチターン対話、プロンプトテンプレート                     | 複雑な意味モデリング、指示制御、チェーンQ&Aをサポート、プロンプトとコンテキストウィンドウを設定可能 |
| エンドツーエンドテストサポート | ✅ 検索+生成プロセスの可視化と指標評価                                | 一体化されたリンクテストツールを提供、リコール的中率、回答カバレッジ、BLEU / ROUGE等の主流指標の評価をサポート |
| デプロイメントモード | ✅ ローカルデプロイメント / Dockerイメージ                          | プライベート化、オフラインデプロイメント、柔軟な運用保守のニーズに対応、高速開発モードをサポート |
| ユーザーインターフェース | ✅ Web UI + RESTful API                              | インタラクティブインターフェースと標準APIインターフェースを提供、Agentモード/通常モードの切り替え、ツール呼び出しプロセスの表示をサポート |
| タスク管理 | ✅ MQ非同期タスク、データベース自動マイグレーション                         | MQによる非同期タスクの状態維持を導入、バージョンアップ時のデータベーステーブル構造とデータの自動マイグレーションをサポート |

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

#### ③ サービスを起動します（Ollama を含む）

.env ファイルで、起動する必要があるイメージを確認します。

```bash
./scripts/start_all.sh
```

または

```bash
make start-all
```

#### ③.0 ollama サービスを起動する (オプション)

```bash
ollama serve > /dev/null 2>&1 &
```

#### ③.1 さまざまな機能の組み合わせを有効にする

- 最小限のコアサービス
```bash
docker compose up -d
```

- すべての機能を有効にする
```bash
docker-compose --profile full up -d
```

- トレースログが必要
```bash
docker-compose --profile jaeger up -d
```

- Neo4j ナレッジグラフが必要
```bash
docker-compose --profile neo4j up -d
```

- Minio ファイルストレージサービスが必要
```bash
docker-compose --profile minio up -d
```

- 複数のオプションの組み合わせ
```bash
docker-compose --profile neo4j --profile minio up -d
```

#### ④ サービスの停止

```bash
./scripts/start_all.sh --stop
# または
make stop-all
```

### 🌐 サービスアクセスアドレス

起動成功後、以下のアドレスにアクセスできます：

* Web UI：`http://localhost`
* バックエンドAPI：`http://localhost:8080`
* リンクトレース（Jaeger）：`http://localhost:16686`

### 🔌 WeChat対話オープンプラットフォームの使用

WeKnoraは[WeChat対話オープンプラットフォーム](https://chatbot.weixin.qq.com)のコア技術フレームワークとして、より簡単な使用方法を提供します：

- **ノーコードデプロイメント**：知識をアップロードするだけで、WeChatエコシステムで迅速にインテリジェントQ&Aサービスをデプロイし、「即座に質問して即座に回答」の体験を実現
- **効率的な問題管理**：高頻度の問題の独立した分類管理をサポートし、豊富なデータツールを提供して、正確で信頼性が高く、メンテナンスが容易な回答を保証
- **WeChatエコシステムカバレッジ**：WeChat対話オープンプラットフォームを通じて、WeKnoraのインテリジェントQ&A能力を公式アカウント、ミニプログラムなどのWeChatシナリオにシームレスに統合し、ユーザーインタラクション体験を向上

### 🔗 MCP サーバーを使用してデプロイ済みの WeKnora にアクセス

#### 1️⃣リポジトリのクローン
```
git clone https://github.com/Tencent/WeKnora
```

#### 2️⃣ MCPサーバーの設定

> 設定には直接 [MCP設定説明](./mcp-server/MCP_CONFIG.md) を参照することをお勧めします。

MCPクライアントでサーバーを設定
```json
{
  "mcpServers": {
    "weknora": {
      "args": [
        "path/to/WeKnora/mcp-server/run_server.py"
      ],
      "command": "python",
      "env":{
        "WEKNORA_API_KEY":"WeKnoraインスタンスに入り、開発者ツールを開いて、リクエストヘッダーx-api-keyを確認、skで始まる",
        "WEKNORA_BASE_URL":"http(s)://あなたのWeKnoraアドレス/api/v1"
      }
    }
  }
}
```

stdioコマンドで直接実行
```
pip install weknora-mcp-server
python -m weknora-mcp-server
```

## 🔧 初期設定ガイド

ユーザーが各種モデルを素早く設定し、試行錯誤のコストを削減するために、元の設定ファイル初期化方法を改善し、Web UIインターフェースを追加して各種モデルの設定を行えるようにしました。使用前に、コードが最新バージョンに更新されていることを確認してください。具体的な使用手順は以下の通りです：
本プロジェクトを初めて使用する場合は、①②の手順をスキップして、直接③④の手順に進んでください。

### ① サービスの停止

```bash
./scripts/start_all.sh --stop
```

### ② 既存のデータテーブルをクリア（重要なデータがない場合の推奨）

```bash
make clean-db
```

### ③ コンパイルしてサービスを起動

```bash
./scripts/start_all.sh
```

### ④ Web UIにアクセス

http://localhost

初回アクセス時は自動的に登録・ログインページに遷移します。登録完了後、新規にナレッジベースを作成し、その設定画面で必要な項目を構成してください。

## 📱 機能デモ

### Web UIインターフェース

<table>
  <tr>
    <td><b>ナレッジベース管理</b><br/><img src="./docs/images/knowledgebases.png" alt="ナレッジベース管理"></td>
    <td><b>対話設定</b><br/><img src="./docs/images/settings.png" alt="対話設定"></td>
  </tr>
  <tr>
    <td colspan="2"><b>Agentモードツール呼び出しプロセス</b><br/><img src="./docs/images/agent-qa.png" alt="Agentモードツール呼び出しプロセス"></td>
  </tr>
</table>

**ナレッジベース管理：** FAQとドキュメントの2種類のナレッジベースの作成をサポート、ドラッグ＆ドロップアップロード、フォルダーインポート、URLインポートなど複数の方法をサポート、文書構造を自動認識してコア知識を抽出し、インデックスを構築します。タグ管理とオンライン入力をサポート、システムは処理の進行状況と文書のステータスを明確に表示し、効率的なナレッジベース管理を実現します。

**Agentモード：** ReACT Agentモードの有効化をサポート、組み込みツールでナレッジベースを検索、ユーザーが設定したMCPツールとWeb検索ツールを呼び出して外部サービスにアクセス、複数回の反復とリフレクションを通じて、最終的に包括的なサマリーレポートを提供します。クロスナレッジベース検索をサポート、複数のナレッジベースを同時に検索できます。

**対話戦略：** Agentモデル、通常モードに必要なモデル、検索閾値の設定をサポート、オンラインPrompt設定をサポート、マルチターン対話の動作と検索リコールの実行方法を精密に制御します。対話入力ボックスはAgentモード/通常モードの切り替えをサポート、Web検索の有効化/無効化をサポート、対話モデルの選択をサポートします。

### 文書ナレッジグラフ

WeKnoraは文書をナレッジグラフに変換し、文書内の異なる段落間の関連関係を表示することをサポートします。ナレッジグラフ機能を有効にすると、システムは文書内部の意味関連ネットワークを分析・構築し、ユーザーが文書内容を理解するのを助けるだけでなく、インデックスと検索に構造化サポートを提供し、検索結果の関連性と幅を向上させます。

詳細な設定については、[ナレッジグラフ設定ガイド](./docs/KnowledgeGraph.md)をご参照ください。

### 対応するMCPサーバー  

[MCP設定ガイド](./mcp-server/MCP_CONFIG.md) をご参照のうえ、必要な設定を行ってください。


## 📘 ドキュメント

よくある問題の解決：[よくある問題](./docs/QA.md)

詳細なAPIドキュメントは：[APIドキュメント](./docs/api/README.md)を参照してください

## 🧭 開発ガイド

### ⚡ 高速開発モード（推奨）

コードを頻繁に変更する必要がある場合、**Dockerイメージを毎回再構築する必要はありません**！高速開発モードを使用してください：

```bash
# 方法1：Makeコマンドを使用（推奨）
make dev-start      # インフラストラクチャを起動
make dev-app        # バックエンドを起動（新しいターミナル）
make dev-frontend   # フロントエンドを起動（新しいターミナル）

# 方法2：ワンクリック起動
./scripts/quick-dev.sh

# 方法3：スクリプトを使用
./scripts/dev.sh start     # インフラストラクチャを起動
./scripts/dev.sh app       # バックエンドを起動（新しいターミナル）
./scripts/dev.sh frontend  # フロントエンドを起動（新しいターミナル）
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

## 👥 コントリビューター

素晴らしいコントリビューターに感謝します：

[![Contributors](https://contrib.rocks/image?repo=Tencent/WeKnora )](https://github.com/Tencent/WeKnora/graphs/contributors )

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
