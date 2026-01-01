import { fetchEventSource } from '@microsoft/fetch-event-source'
import { ref, type Ref, onUnmounted, nextTick } from 'vue'
import { generateRandomString } from '@/utils/index';



interface StreamOptions {
  // 요청 방법 (기본값 POST)
  method?: 'GET' | 'POST'
  // 요청 헤더
  headers?: Record<string, string>
  // 요청 본문 자동 직렬화
  body?: Record<string, any>
  // 스트리밍 렌더링 간격 (ms)
  chunkInterval?: number
}

export function useStream() {
  // 반응형 상태
  const output = ref('')              // 표시 내용
  const isStreaming = ref(false)      // 스트리밍 상태
  const isLoading = ref(false)        // 초기 로딩
  const error = ref<string | null>(null)// 오류 메시지
  let controller = new AbortController()

  // 스트리밍 렌더링 버퍼
  let buffer: string[] = []
  let renderTimer: number | null = null

  // 스트리밍 요청 시작
  const startStream = async (params: { session_id: any; query: any; knowledge_base_ids?: string[]; knowledge_ids?: string[]; agent_enabled?: boolean; agent_id?: string; web_search_enabled?: boolean; summary_model_id?: string; mcp_service_ids?: string[]; mentioned_items?: Array<{id: string; name: string; type: string; kb_type?: string}>; method: string; url: string }) => {
    // 상태 초기화
    output.value = '';
    error.value = null;
    isStreaming.value = true;
    isLoading.value = true;

    // API 구성 가져오기
    const apiUrl = import.meta.env.VITE_IS_DOCKER ? "" : "http://localhost:8080";
    
    // JWT 토큰 가져오기
    const token = localStorage.getItem('weknora_token');
    if (!token) {
      error.value = "로그인 토큰을 찾을 수 없습니다. 다시 로그인해 주세요.";
      stopStream();
      return;
    }

    // 테넌트 간 액세스 요청 헤더 가져오기
    const selectedTenantId = localStorage.getItem('weknora_selected_tenant_id');
    const defaultTenantId = localStorage.getItem('weknora_tenant');
    let tenantIdHeader: string | null = null;
    if (selectedTenantId) {
      try {
        const defaultTenant = defaultTenantId ? JSON.parse(defaultTenantId) : null;
        const defaultId = defaultTenant?.id ? String(defaultTenant.id) : null;
        if (selectedTenantId !== defaultId) {
          tenantIdHeader = selectedTenantId;
        }
      } catch (e) {
        console.error('Failed to parse tenant info', e);
      }
    }

    // Validate knowledge_base_ids for agent-chat requests
    // Note: knowledge_base_ids can be empty if user hasn't selected any, but we allow it
    // The backend will handle the case when no knowledge bases are selected
    const isAgentChat = params.url === '/api/v1/agent-chat';
    // Removed validation - allow empty knowledge_base_ids array
    // The backend should handle this case appropriately

    try {
      let url =
        params.method == "POST"
          ? `${apiUrl}${params.url}/${params.session_id}`
          : `${apiUrl}${params.url}/${params.session_id}?message_id=${params.query}`;
      
      // Prepare POST body with required fields for agent-chat
      // knowledge_base_ids array and agent_enabled can update Session's SessionAgentConfig
      const postBody: any = { 
        query: params.query,
        agent_enabled: params.agent_enabled !== undefined ? params.agent_enabled : true
      };
      // Always include knowledge_base_ids for agent-chat (already validated above)
      if (params.knowledge_base_ids !== undefined && params.knowledge_base_ids.length > 0) {
        postBody.knowledge_base_ids = params.knowledge_base_ids;
      }
      // Include knowledge_ids if provided
      if (params.knowledge_ids !== undefined && params.knowledge_ids.length > 0) {
        postBody.knowledge_ids = params.knowledge_ids;
      }
      // Include agent_id if provided (for custom agent configuration)
      if (params.agent_id) {
        postBody.agent_id = params.agent_id;
      }
      // Include web_search_enabled if provided
      if (params.web_search_enabled !== undefined) {
        postBody.web_search_enabled = params.web_search_enabled;
      }
      // Include summary_model_id if provided (for non-Agent mode)
      if (params.summary_model_id) {
        postBody.summary_model_id = params.summary_model_id;
      }
      // Include mcp_service_ids if provided (for Agent mode)
      if (params.mcp_service_ids !== undefined && params.mcp_service_ids.length > 0) {
        postBody.mcp_service_ids = params.mcp_service_ids;
      }
      // Include mentioned_items if provided (for displaying @mentions in chat)
      if (params.mentioned_items !== undefined && params.mentioned_items.length > 0) {
        postBody.mentioned_items = params.mentioned_items;
      }
      
      await fetchEventSource(url, {
        method: params.method,
        headers: {
          "Content-Type": "application/json",
          "Authorization": `Bearer ${token}`,
          "X-Request-ID": `${generateRandomString(12)}`,
          ...(tenantIdHeader ? { "X-Tenant-ID": tenantIdHeader } : {}),
        },
        body:
          params.method == "POST"
            ? JSON.stringify(postBody)
            : null,
        signal: controller.signal,
        openWhenHidden: true,

        onopen: async (res) => {
          if (!res.ok) throw new Error(`HTTP ${res.status}`);
          isLoading.value = false;
        },

        onmessage: (ev) => {
          buffer.push(JSON.parse(ev.data)); // 데이터 버퍼에 저장
          // 사용자 정의 처리 실행
          if (chunkHandler) {
            chunkHandler(JSON.parse(ev.data));
          }
        },

        onerror: (err) => {
          throw new Error(`스트리밍 연결 실패: ${err}`);
        },

        onclose: () => {
          stopStream();
        },
      });
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
      stopStream()
    }
  }

  let chunkHandler: ((data: any) => void) | null = null
  // 청크 처리기 등록
  const onChunk = (handler: () => void) => {
    chunkHandler = handler
  }


  // 스트리밍 중지
  const stopStream = () => {
    controller.abort();
    controller = new AbortController(); // 컨트롤러 재설정 (다시 시작해야 할 경우)
    isStreaming.value = false;
    isLoading.value = false;
  }

  // 컴포넌트 마운트 해제 시 자동 정리
  onUnmounted(stopStream)

  return {
    output,          // 표시 내용
    isStreaming,     // 스트리밍 전송 중인지 여부
    isLoading,       // 초기 연결 상태
    error,
    onChunk,
    startStream,     // 스트리밍 시작
    stopStream       // 수동 중지
  }
}
