<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { createKnowledgeFolder, deleteKnowledgeFolder, listKnowledgeFolders, updateKnowledgeFolder, type KnowledgeFolder } from '@/api/knowledge-base';
import { useI18n } from 'vue-i18n';
import KnowledgeFolderPicker from './KnowledgeFolderPicker.vue';
import { flattenFolderPages, mergeFolderPage, type FolderPage, type FolderTreeRow } from '@/utils/knowledgeFolders';

const props = defineProps<{ kbId:string; modelValue:string; canEdit:boolean; agentId?:string }>();
const emit = defineEmits<{ 'update:modelValue':[value:string]; ask:[folder:KnowledgeFolder]; changed:[] }>();
const { t } = useI18n();
const PAGE_SIZE = 50;
type PageState = FolderPage<KnowledgeFolder>;
type Row = FolderTreeRow<KnowledgeFolder>;
const pages = ref(new Map<string,PageState>());
const expanded = ref(new Set<string>());
const editorVisible=ref(false), editorName=ref(''), editorParent=ref(''), editorTarget=ref<KnowledgeFolder|null>(null);
const moveVisible=ref(false), moveTarget=ref(''), movingFolder=ref<KnowledgeFolder|null>(null);

async function load(parentId='', reset=false) {
  const current=pages.value.get(parentId);
  if(current?.loading)return;
  if(!reset&&current&&current.items.length>=current.total)return;
  const page=reset?1:(current?.page||0)+1;
  pages.value.set(parentId,{items:reset?[]:current?.items||[],page:page-1,total:current?.total||0,loading:true});pages.value=new Map(pages.value);
  try {
    const res:any=await listKnowledgeFolders(props.kbId,{parent_id:parentId,page,page_size:PAGE_SIZE,agent_id:props.agentId});
    const result=res?.data||{};const next=Array.isArray(result.data)?result.data:[];
    pages.value.set(parentId,mergeFolderPage(current,next,page,result.total,reset));
  } finally { const state=pages.value.get(parentId);if(state?.loading)pages.value.set(parentId,{...state,loading:false});pages.value=new Map(pages.value); }
}
const rows=computed<Row[]>(()=>flattenFolderPages(pages.value,expanded.value));
async function toggle(folder:KnowledgeFolder){if(!folder.has_children)return;if(expanded.value.has(folder.id))expanded.value.delete(folder.id);else{expanded.value.add(folder.id);await load(folder.id)};expanded.value=new Set(expanded.value)}
function openCreate(parent=''){editorTarget.value=null;editorParent.value=parent;editorName.value='';editorVisible.value=true}
function openRename(folder:KnowledgeFolder){editorTarget.value=folder;editorParent.value=folder.parent_id;editorName.value=folder.name;editorVisible.value=true}
async function save(){if(!editorName.value.trim())return;try{if(editorTarget.value)await updateKnowledgeFolder(props.kbId,editorTarget.value.id,{name:editorName.value});else await createKnowledgeFolder(props.kbId,{name:editorName.value,parent_id:editorParent.value});editorVisible.value=false;await load(editorParent.value,true);emit('changed')}catch(error:any){MessagePlugin.error(error?.message||String(error))}}
async function remove(folder:KnowledgeFolder){try{await deleteKnowledgeFolder(props.kbId,folder.id);await load(folder.parent_id,true);if(props.modelValue===folder.id)emit('update:modelValue','');emit('changed')}catch(error:any){MessagePlugin.error(error?.message||t('knowledgeFolder.emptyRequired'))}}
function openMove(folder:KnowledgeFolder){movingFolder.value=folder;moveTarget.value=folder.parent_id;moveVisible.value=true}
async function saveMove(){if(!movingFolder.value)return;try{await updateKnowledgeFolder(props.kbId,movingFolder.value.id,{parent_id:moveTarget.value});moveVisible.value=false;await load(movingFolder.value.parent_id,true);await load(moveTarget.value,true);emit('changed')}catch(error:any){MessagePlugin.error(error?.message||String(error))}}
function folderMenuOptions(){const ask={content:t('knowledgeFolder.ask'),value:'ask'};return props.canEdit?[{content:t('knowledgeFolder.newFolder'),value:'create'},{content:t('knowledgeFolder.rename'),value:'rename'},{content:t('knowledgeFolder.move'),value:'move'},ask,{content:t('knowledgeFolder.delete'),value:'delete'}]:[ask]}
function menu(folder:KnowledgeFolder,data:any){const action=data?.value||data;if(action==='create')openCreate(folder.id);if(action==='rename')openRename(folder);if(action==='move')openMove(folder);if(action==='ask')emit('ask',folder);if(action==='delete')remove(folder)}
async function refresh(){const parents=['',...expanded.value];await Promise.all(parents.map(parent=>load(parent,true)))}
watch(()=>[props.kbId,props.agentId],()=>{pages.value=new Map();expanded.value=new Set();void load('',true)});
onMounted(()=>void load('',true));
defineExpose({ refresh });
</script>
<template>
  <aside class="folder-tree" :aria-label="t('knowledgeFolder.folders')">
    <div class="folder-tree__header"><strong>{{ t('knowledgeFolder.folders') }}</strong><t-button v-if="canEdit" shape="square" variant="text" size="small" :title="t('knowledgeFolder.newFolder')" @click="openCreate('')"><t-icon name="folder-add" /></t-button></div>
    <button class="folder-row" :class="{active:modelValue===''}" @click="emit('update:modelValue','')"><span class="folder-indent"/><t-icon name="home"/><span class="folder-name">{{ t('knowledgeFolder.root') }}</span></button>
    <template v-for="(row,index) in rows" :key="row.kind==='folder'?row.folder.id:`more-${row.parentId}-${index}`">
      <div v-if="row.kind==='folder'" class="folder-row" :class="{active:modelValue===row.folder.id}" :style="{paddingLeft:`${8+row.depth*18}px`}" @click="emit('update:modelValue',row.folder.id)">
        <button class="folder-expand" :disabled="!row.folder.has_children" :title="row.folder.name" @click.stop="toggle(row.folder)"><t-icon :name="row.folder.has_children?(expanded.has(row.folder.id)?'chevron-down':'chevron-right'):''"/></button><t-icon name="folder"/><span class="folder-name">{{ row.folder.name }}</span><span class="folder-count">{{ row.folder.total_knowledge_count }}</span>
        <t-dropdown trigger="click" :options="folderMenuOptions()" @click="(data:any)=>menu(row.folder,data)"><t-button shape="square" variant="text" size="small" @click.stop><t-icon name="more"/></t-button></t-dropdown>
      </div>
      <t-button v-else variant="text" size="small" :loading="pages.get(row.parentId)?.loading" :style="{marginLeft:`${26+row.depth*18}px`}" @click="load(row.parentId)">{{ t('knowledgeFolder.loadMore') }}</t-button>
    </template>
    <t-dialog v-model:visible="editorVisible" :header="editorTarget?t('knowledgeFolder.rename'):t('knowledgeFolder.newFolder')" :confirm-btn="t('common.confirm')" @confirm="save"><t-input v-model="editorName" :maxlength="100" autofocus /></t-dialog>
	<t-dialog v-model:visible="moveVisible" :header="t('knowledgeFolder.move')" :confirm-btn="t('common.confirm')" @confirm="saveMove"><KnowledgeFolderPicker v-if="moveVisible" v-model="moveTarget" :kb-id="kbId" :disabled-ids="movingFolder?[movingFolder.id]:[]" /></t-dialog>
  </aside>
</template>
<style scoped>
.folder-tree{box-sizing:border-box;width:240px;min-width:200px;border-right:1px solid var(--td-component-stroke);padding:10px 8px;overflow:auto;background:var(--td-bg-color-container)}.folder-tree__header{height:36px;display:flex;align-items:center;justify-content:space-between;padding:0 6px}.folder-row{box-sizing:border-box;width:100%;height:34px;display:flex;align-items:center;gap:7px;border:0;background:transparent;color:var(--td-text-color-primary);font:inherit;text-align:left;padding:0 8px;cursor:pointer}.folder-row:hover,.folder-row.active{background:var(--td-bg-color-container-hover)}.folder-row.active{color:var(--td-brand-color)}.folder-expand{width:18px;height:24px;border:0;background:transparent;padding:0;flex:none}.folder-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1}.folder-count{font-size:12px;color:var(--td-text-color-placeholder)}.folder-indent{width:18px}@media(max-width:760px){.folder-tree{width:100%;max-width:100%;height:100%}}
</style>
