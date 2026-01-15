import { ref, reactive } from "vue";
import { storeToRefs } from "pinia";
import { formatStringDate, kbFileTypeVerification } from "../utils/index";
import { MessagePlugin } from "tdesign-vue-next";
import {
  uploadKnowledgeFile,
  listKnowledgeFiles,
  getKnowledgeDetails,
  delKnowledgeDetails,
  getKnowledgeDetailsCon,
} from "@/api/knowledge-base/index";
import { knowledgeStore } from "@/stores/knowledge";
import { useUIStore } from "@/stores/ui";
import { useRoute } from 'vue-router';

const usemenuStore = knowledgeStore();
export default function (knowledgeBaseId?: string) {
  const route = useRoute();
  const { cardList, total } = storeToRefs(usemenuStore);
  let moreIndex = ref(-1);
  const details = reactive({
    title: "",
    time: "",
    md: [] as any[],
    id: "",
    total: 0,
    type: "",
    source: "",
    file_type: ""
  });
  const getKnowled = (
    query: { page: number; page_size: number; tag_id?: string; keyword?: string; file_type?: string } = { page: 1, page_size: 35 },
    kbId?: string,
  ) => {
    const targetKbId = kbId || knowledgeBaseId;
    if (!targetKbId) return;
    
    listKnowledgeFiles(targetKbId, query)
      .then((result: any) => {
        const { data, total: totalResult } = result;
    const cardList_ = data.map((item: any) => {
      const rawName = item.file_name || item.title || item.source || '未命名文档'
      const dotIndex = rawName.lastIndexOf('.')
      const displayName = dotIndex > 0 ? rawName.substring(0, dotIndex) : rawName
      const fileTypeSource = item.file_type || (item.type === 'manual' ? 'MANUAL' : '')
      return {
        ...item,
        original_file_name: item.file_name,
        display_name: displayName,
        file_name: displayName,
        updated_at: formatStringDate(new Date(item.updated_at)),
        isMore: false,
        file_type: fileTypeSource ? String(fileTypeSource).toLocaleUpperCase() : '',
      }
    });
        
        if (query.page === 1) {
          cardList.value = cardList_;
        } else {
          cardList.value.push(...cardList_);
        }
        total.value = totalResult;
      })
      .catch(() => {});
  };
  const delKnowledge = (index: number, item: any, onSuccess?: () => void) => {
    cardList.value[index].isMore = false;
    moreIndex.value = -1;
    return delKnowledgeDetails(item.id)
      .then((result: any) => {
        if (result.success) {
          MessagePlugin.info("知识删除成功！");
          if (onSuccess) {
            onSuccess();
          } else {
            getKnowled();
          }
          return true;
        } else {
          MessagePlugin.error("知识删除失败！");
          return false;
        }
      })
      .catch(() => {
        MessagePlugin.error("知识删除失败！");
        return false;
      });
  };
  const openMore = (index: number) => {
    moreIndex.value = index;
  };
  const onVisibleChange = (visible: boolean) => {
    if (!visible) {
      moreIndex.value = -1;
    }
  };
  const requestMethod = (file: any, uploadInput: any) => {
    if (!(file instanceof File) || !uploadInput) {
      MessagePlugin.error("文件类型错误！");
      return;
    }
    
    if (kbFileTypeVerification(file)) {
      return;
    }
    
    // 获取当前知识库ID
    let currentKbId: string | undefined = (route.params as any)?.kbId as string;
    if (!currentKbId && typeof window !== 'undefined') {
      const match = window.location.pathname.match(/knowledge-bases\/([^/]+)/);
      if (match?.[1]) currentKbId = match[1];
    }
    if (!currentKbId) {
      currentKbId = knowledgeBaseId;
    }
    if (!currentKbId) {
      MessagePlugin.error("缺少知识库ID");
      return;
    }
    
    // 获取当前选中的分类ID
    const uiStore = useUIStore();
    const tagIdToUpload = uiStore.selectedTagId !== '__untagged__' ? uiStore.selectedTagId : undefined;
    
    uploadKnowledgeFile(currentKbId, { file, tag_id: tagIdToUpload })
      .then((result: any) => {
        if (result.success) {
          MessagePlugin.info("上传成功！");
          getKnowled({ page: 1, page_size: 35 }, currentKbId);
        } else {
          const errorMessage = result.error?.message || result.message || "上传失败！";
          MessagePlugin.error(result.code === 'duplicate_file' ? "文件已存在" : errorMessage);
        }
        uploadInput.value.value = "";
      })
      .catch((err: any) => {
        const errorMessage = err.error?.message || err.message || "上传失败！";
        MessagePlugin.error(err.code === 'duplicate_file' ? "文件已存在" : errorMessage);
        uploadInput.value.value = "";
      });
  };
  const getCardDetails = (item: any) => {
    Object.assign(details, {
      title: "",
      time: "",
      md: [],
      id: "",
      type: "",
      source: "",
      file_type: ""
    });
    getKnowledgeDetails(item.id)
      .then((result: any) => {
        if (result.success && result.data) {
          const { data } = result;
          Object.assign(details, {
            title: data.file_name || data.title || data.source || '未命名文档',
            time: formatStringDate(new Date(data.updated_at)),
            id: data.id,
            type: data.type || 'file',
            source: data.source || '',
            file_type: data.file_type || ''
          });
        }
      })
      .catch(() => {});
    getfDetails(item.id, 1);
  };
  
  const getfDetails = (id: string, page: number) => {
    getKnowledgeDetailsCon(id, page)
      .then((result: any) => {
        if (result.success && result.data) {
          const { data, total: totalResult } = result;
          if (page === 1) {
            details.md = data;
          } else {
            details.md.push(...data);
          }
          details.total = totalResult;
        }
      })
      .catch(() => {});
  };
  return {
    cardList,
    moreIndex,
    getKnowled,
    details,
    delKnowledge,
    openMore,
    onVisibleChange,
    requestMethod,
    getCardDetails,
    total,
    getfDetails,
  };
}
