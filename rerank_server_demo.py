import torch
import uvicorn
from fastapi import FastAPI
from pydantic import BaseModel, Field
from transformers import AutoModelForSequenceClassification, AutoTokenizer
from typing import List

# --- 1. 定义API的请求和响应数据结构 ---

# 请求体结构保持不变
class RerankRequest(BaseModel):
    query: str
    documents: List[str]

# --- 修改开始：定义测试用的响应结构，字段名为 "score" ---

# DocumentInfo 结构保持不变
class DocumentInfo(BaseModel):
    text: str

# 将原来的 GoRankResult 修改为 TestRankResult
# 核心改动：将 "relevance_score" 字段重命名为 "score"
class TestRankResult(BaseModel):
    index: int
    document: DocumentInfo
    score: float  # <--- 【关键修改点】字段名已从 relevance_score 改为 score

# 最终响应体结构，其 "results" 列表包含的是 TestRankResult
class TestFinalResponse(BaseModel):
    results: List[TestRankResult]

# --- 修改结束 ---


# --- 2. 加载模型 (在服务启动时执行一次) ---
print("正在加载模型，请稍候...")
device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
print(f"使用的设备: {device}")
try:
    # 请确保这里的路径是正确的
    model_path = '/data1/home/lwx/work/Download/rerank_model_weight'
    tokenizer = AutoTokenizer.from_pretrained(model_path)
    model = AutoModelForSequenceClassification.from_pretrained(model_path)
    model.to(device)
    model.eval()
    print("模型加载成功！")
except Exception as e:
    print(f"模型加载失败: {e}")
    # 在测试环境中，如果模型加载失败，可以考虑退出以避免运行一个无效的服务
    exit()

# --- 3. 创建FastAPI应用 ---
app = FastAPI(
    title="Reranker API (Test Version)",
    description="一个返回 'score' 字段以测试Go客户端兼容性的API服务",
    version="1.0.1"
)

# --- 4. 定义API端点 ---
# --- 修改开始：将 response_model 指向新的测试用响应结构 ---
@app.post("/rerank", response_model=TestFinalResponse) # <--- 【关键修改点】response_model 改为 TestFinalResponse
def rerank_endpoint(request: RerankRequest):
    # --- 修改结束 ---

    pairs = [[request.query, doc] for doc in request.documents]

    with torch.no_grad():
        inputs = tokenizer(pairs, padding=True, truncation=True, return_tensors='pt', max_length=1024).to(device)
        scores = model(**inputs, return_dict=True).logits.view(-1, ).float()

    # --- 修改开始：按照测试用的结构来构建结果 ---
    results = []
    for i, (text, score_val) in enumerate(zip(request.documents, scores)):
        
        # 1. 创建嵌套的 document 对象
        doc_info = DocumentInfo(text=text)
        
        # 2. 创建 TestRankResult 对象
        #    注意字段名：index, document, score
        test_result = TestRankResult(
            index=i,
            document=doc_info,
            score=score_val.item()  # <--- 【关键修改点】赋值给 "score" 字段
        )
        results.append(test_result)

    # 3. 排序 (key 也要相应修改为 score)
    sorted_results = sorted(results, key=lambda x: x.score, reverse=True)
    # --- 修改结束 ---
    
    # 返回一个字典，FastAPI 会根据 response_model (TestFinalResponse) 来验证和序列化它
    # 最终生成的 JSON 会是 {"results": [{"index": ..., "document": ..., "score": ...}]}
    return {"results": sorted_results}

@app.get("/")
def read_root():
    return {"status": "Reranker API (Test Version) is running"}

# --- 5. 启动服务 ---
if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8000)
    