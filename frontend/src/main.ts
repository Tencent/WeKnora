import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import router from "./router";
import "./assets/fonts.css";
import TDesign from "tdesign-vue-next";
// 컴포넌트 라이브러리의 소량의 전역 스타일 변수 가져오기
import "tdesign-vue-next/es/style/index.css";
import "@/assets/theme/theme.css";
import i18n from "./i18n";

const app = createApp(App);

app.use(TDesign);
app.use(createPinia());
app.use(router);
app.use(i18n);

app.mount("#app");
