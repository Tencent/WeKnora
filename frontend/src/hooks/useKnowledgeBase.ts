import { ref, reactive, onMounted } from "vue";
import { storeToRefs } from "pinia";
import { formatStringDate, kbFileTypeVerification } from "../utils/index";
import { MessagePlugin } from "tdesign-vue-next";
import {
  uploadKnowledgeBase,
  getKnowledgeBase,
  getKnowledgeDetails,
  delKnowledgeDetails,
  getKnowledgeDetailsCon,
} from "@/api/knowledge-base/index";
import { knowledgeStore } from "@/stores/knowledge";
const usemenuStore = knowledgeStore();
export default function () {
  const { cardList, total } = storeToRefs(usemenuStore);
  let moreIndex = ref(-1);
  const details = reactive({
    title: "",
    time: "",
    md: [],
    id: "",
    total: 0
  });
  const getKnowled = (query = { page: 1, page_size: 35 }) => {
    getKnowledgeBase(query)
      .then((result: any) => {
        let { data, total: totalResult } = result;
        let cardList_ = data.map((item) => {
            item["file_name"] = item.file_name.substring(
              0,
              item.file_name.lastIndexOf(".")
            );
            return {
              ...item,
              updated_at: formatStringDate(new Date(item.updated_at)),
              isMore: false,
              file_type: item.file_type.toLocaleUpperCase(),
            };
        });
        if (query.page == 1) {
          cardList.value = cardList_;
        } else {
          cardList.value.push(...cardList_);
        }
        total.value = totalResult;
      })
      .catch((err) => {});
  };
  const delKnowledge = (index: number, item) => {
    cardList.value[index].isMore = false;
    moreIndex.value = -1;
    delKnowledgeDetails(item.id)
      .then((result: any) => {
        if (result.success) {
          MessagePlugin.info("知识删除成功！");
          getKnowled();
        } else {
          MessagePlugin.error("知识删除失败！");
        }
      })
      .catch((err) => {
        MessagePlugin.error("知识删除失败！");
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
  const requestMethod = (file: any, uploadInput) => {
    if (file instanceof File && uploadInput) {
      if (kbFileTypeVerification(file)) {
        return;
      }
      uploadKnowledgeBase({ file })
        .then((result: any) => {
          if (result.success) {
            MessagePlugin.info("上传成功！");
            getKnowled();
          } else {
            // 检查错误码，如果是重复文件则显示特定提示
            if (result.code === 'duplicate_file') {
              MessagePlugin.error("文件已存在");
            } else {
              MessagePlugin.error(result.message || (result.error && result.error.message) || "上传失败！");
            }
          }
          uploadInput.value.value = "";
        })
        .catch((err: any) => {
          // 检查错误响应中的错误码
          if (err.code === 'duplicate_file') {
            MessagePlugin.error("文件已存在");
          } else if (err.message) {
            MessagePlugin.error(err.message);
          } else {
            MessagePlugin.error("上传失败！");
          }
          uploadInput.value.value = "";
        });
    } else {
      MessagePlugin.error("file文件类型错误！");
    }
  };
  const getCardDetails = (item) => {
    Object.assign(details, {
      title: "",
      time: "",
      md: [],
      id: "",
    });
    getKnowledgeDetails(item.id)
      .then((result: any) => {
        if (result.success && result.data) {
          let { data } = result;
          Object.assign(details, {
            title: data.file_name,
            time: formatStringDate(new Date(data.updated_at)),
            id: data.id,
          });
        }
      })
      .catch((err) => {});
      getfDetails(item.id, 1);
  };
  const getfDetails = (id, page) => {
    getKnowledgeDetailsCon(id, page)
      .then((result: any) => {
        if (result.success && result.data) {
          let { data, total: totalResult } = result;
          if (page == 1) {
            details.md = data;
          } else {
            details.md.push(...data);
          }
          details.total = totalResult;
        }
      })
      .catch((err) => {});
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
