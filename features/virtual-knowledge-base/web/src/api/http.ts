import axios from "axios";

export const apiClient = axios.create({
  baseURL: "/api/v1/virtual-kb",
  timeout: 15000,
  headers: {
    "Content-Type": "application/json",
  },
});

export const setAPIKey = (apiKey?: string) => {
  if (apiKey) {
    apiClient.defaults.headers.common["X-API-Key"] = apiKey;
  } else {
    delete apiClient.defaults.headers.common["X-API-Key"];
  }
};
