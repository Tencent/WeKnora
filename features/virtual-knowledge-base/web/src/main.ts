import { createApp } from "vue";
import { createPinia } from "pinia";
import TDesign from "tdesign-vue-next";

import App from "./App.vue";
import { createVirtualKBRouter } from "./router";

import "tdesign-vue-next/es/style/index.css";

const app = createApp(App);

app.use(createPinia());
app.use(TDesign);
app.use(createVirtualKBRouter());

app.mount("#app");
