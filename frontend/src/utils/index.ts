import { MessagePlugin } from "tdesign-vue-next";
export function generateRandomString(length: number) {
  let result = "";
  const characters =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  const charactersLength = characters.length;
  for (let i = 0; i < length; i++) {
    result += characters.charAt(Math.floor(Math.random() * charactersLength));
  }
  return result;
}

export function formatStringDate(date: any) {
  let data = new Date(date);
  let year = data.getFullYear();
  let month = String(data.getMonth() + 1).padStart(2, '0');
  let day = String(data.getDate()).padStart(2, '0');
  let hour = String(data.getHours()).padStart(2, '0');
  let minute = String(data.getMinutes()).padStart(2, '0');
  let second = String(data.getSeconds()).padStart(2, '0');
  return (
    year + "-" + month + "-" + day + " " + hour + ":" + minute + ":" + second
  );
}
export function kbFileTypeVerification(file: any) {
  let validTypes = ["pdf", "txt", "md", "docx", "doc", "jpg", "jpeg", "png", "csv", "xlsx", "xls"];
  let type = file.name.substring(file.name.lastIndexOf(".") + 1);
  if (!validTypes.includes(type)) {
    MessagePlugin.error("文件类型错误！");
    return true;
  }
  if (
    (type == "pdf" || type == "docx" || type == "doc") &&
    file.size > 31457280
  ) {
    MessagePlugin.error("pdf/doc文件不能超过30M！");
    return true;
  }
  if ((type == "txt" || type == "md") && file.size > 31457280) {
    MessagePlugin.error("txt/md文件不能超过30M！");
    return true;
  }
  return false
}
