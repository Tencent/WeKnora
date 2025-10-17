import { ref } from "vue";

export function useAsync<T>(initialData: T) {
  const data = ref<T>(initialData);
  const loading = ref(false);
  const error = ref<string | null>(null);

  const run = async (fn: () => Promise<T>) => {
    loading.value = true;
    error.value = null;
    try {
      const result = await fn();
      data.value = result;
      return result;
    } catch (err) {
      error.value = (err as Error).message ?? "Request failed";
      throw err;
    } finally {
      loading.value = false;
    }
  };

  const setData = (value: T) => {
    data.value = value;
  };

  return {
    data,
    loading,
    error,
    run,
    setData,
  };
}
