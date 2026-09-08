import type {
  CrossVideoPayload,
  GraphEdge,
  GraphEvidence,
  GraphKnowledgeDetail,
  KnowledgeGraphDetailPayload,
  KnowledgeGraphPayload,
  KnowledgeStructureField,
  KnowledgeType,
  RelationType,
  WikiGraphRequest,
} from '@/types/videohub'
import type { RelatedKnowledgePayload } from '../relatedKnowledge'

export const AI_LEARNING_EVAL_FIXTURE = 'ai-learning'
export const AI_LEARNING_EVAL_VIDEO_ID = '8a6bad62-551e-4370-9f52-52886dfe4bc2'

const VIDEO_TITLE = '我们应该如何学习 AI？'
const TRANSCRIPT_GENERATION = '2600031422-WorkflowTask-db2fd27921c668a54576081b5b3b762att7'

interface FixtureEvidence {
  id: string
  startMs: number
  endMs: number
  text: string
}

interface FixtureObject {
  key: string
  type: KnowledgeType
  title: string
  core: string
  timeRange: string
  confidence: number
  fields: Array<[string, string, string]>
  evidenceIds: string[]
  entitySubType?: string
}

interface FixtureRelation {
  id: string
  source: string
  target: string
  type: RelationType
  evidenceIds: string[]
  confidence: number
}

const evidence: FixtureEvidence[] = [
  { id: 'EV001', startMs: 165, endMs: 18915, text: '最近我在日本的一个书店发现他们卖很多教你怎么用 Cloud Code 的书……就拍了个照片，然后发了个 Twitter……这条帖子特别火，已经 60 多万浏览了。' },
  { id: 'EV002', startMs: 20990, endMs: 35160, text: '有人说他曾经在出版社工作，当时在做一本教 Photoshop 的书，排版还没排完呢，Photoshop 功能就已经变了……大部分买这类书的人其实也并不会真正地去读或者上手，他们更多是想买一种在学习的感觉。' },
  { id: 'EV003', startMs: 35260, endMs: 76863, text: '要想学会用 AI，你必须动手实操……现在技术的迭代都是以天为单位的，等它被印成书的时候早就过时了……我们应该去从书里学一些不变的东西，一些更经典的东西。' },
  { id: 'EV004', startMs: 86688, endMs: 119188, text: '你不应该把 AI 看成是一个学科，除非你是搞研究的……你要认为它更像是学开车或者学游泳……学习 Codex 的唯一方法就是直接去用 Codex。所以我们的心态要从 learn to build 变成 build to learn。' },
  { id: 'EV005', startMs: 119850, endMs: 140000, text: '我的方法并不是找一堆编程基础课去看，而是直接开始让 Cloud Code 或者 Codex 给我做一些小产品……做的过程中有不懂的再问 AI……做了十个这样的小产品之后，你会发现你对 AI coding 的理解会达到 next level。' },
  { id: 'EV006', startMs: 140000, endMs: 169000, text: '先做一些给自己提效的小工具……以学习和探索为目的去做。这是一种 on demand learning，也就是先创造场景和需求，然后需要的时候再现学……从传统教育的先理论再实操变成先实操再理论。' },
  { id: 'EV007', startMs: 169000, endMs: 201000, text: '把你学习的过程和心得分享出来……能给你带来持续学习的动力……人要长期坚持一件事情并不是靠毅力，而是靠反馈……这会给你带来一种身份认同。' },
  { id: 'EV008', startMs: 203000, endMs: 218000, text: '前几天我突然很缺乏动力，然后我就去跟 Claude 聊这件事儿……Motivation follows action more than it precedes it。也就是说人不是因为有了动力才去行动，而是因为行动了才带来了动力。' },
  { id: 'EV009', startMs: 218000, endMs: 239000, text: '很多时候大家觉得没有动力学习，这本身就会成为一个负向循环……learn in public 可以帮你快速进入到一个正循环里面，让你的学习有一些及时反馈。' },
  { id: 'EV010', startMs: 242000, endMs: 278000, text: '第一是跟你背景类似，但是稍微比你超前一点的人……如果你持续地通过 learn in public 的方式给周围的人无私地带来价值，他们也会很愿意教你各种东西。' },
  { id: 'EV011', startMs: 278000, endMs: 310000, text: '第二类老师就是顶尖的行业从业者和 builder……最好的平台就是 Twitter 和 YouTube……这些信息……来自实际在做业务的人的一手经验。' },
  { id: 'EV012', startMs: 310000, endMs: 337000, text: '在互联网上学习最重要的事就是找到正确的信息源……我自己的 Twitter 已经被我调教成了一个 AI 学习平台……建议大家去主动训练算法，让算法给你推高质量干货的内容。' },
  { id: 'EV013', startMs: 339000, endMs: 382000, text: '这里教大家一个方法叫顺藤摸瓜……去看他关注了哪些人……再去看 Andrew 的关注列表……怎么判断这些人值不值得关注呢？你就是去看他的个人简介……再去跟他们点赞、互动，慢慢算法就会给你推更多类似的人。' },
  { id: 'EV014', startMs: 383000, endMs: 409000, text: 'AI 行业变化太快了，而且这些从业者，包括研究员、开发者、创业者等等全都在 X 上……你在 X 上能看到最新的、最一手的信息，但是前提是你要关注正确的人，把你的算法调教好。' },
  { id: 'EV015', startMs: 409000, endMs: 438000, text: '我其实很多认知是通过我输出内容之后，通过反馈学习到的……我发了一些 vibe coding 的产品之后，很多用户会给我提各种反馈或者问题……我就知道了大家关心什么，或者用户实际的需求和痛点是什么。' },
  { id: 'EV016', startMs: 438000, endMs: 455000, text: '我们应该去想办法创造一些正反馈，创造一些跟外界接触的机会，然后把学习变成一件愉悦的事情……这样它才是可持续的。' },
  { id: 'EV017', startMs: 455000, endMs: 475000, text: '你可以让 AI 教你用 AI……可以完全用小白的语言、极其有耐心地给你讲解，24 小时永远在线……把 AI 变编程老师是让人很有心理安全感的一种学习方式。' },
  { id: 'EV018', startMs: 475000, endMs: 503000, text: '用一个语音输入法给它详细地描述你的工作内容、你日常的场景和痛点……一起去 brainstorm 它可以帮你做什么……一起跟它把它做出来……让 AI 把项目的架构画成一个 HTML 的图片，帮你理解，甚至做成一个课程给你出题。' },
  { id: 'EV019', startMs: 503000, endMs: 535873, text: '以前那些看不懂的内容也都可以让 AI 去陪你看……你直接大白话问……大家关于 AI 99% 的问题都可以通过直接问 AI 来解决……你要善于提问、勤提问，真的把 AI 当成老师去刨根问底。' },
]

const objects: FixtureObject[] = [
  { key: 'K001', type: 'entity', entitySubType: 'product', title: 'X（Twitter）', core: 'X（Twitter）是视频中用于追踪 AI 从业者和一手行业信息的平台。', timeRange: '05:10.000-06:49.000', confidence: .96, evidenceIds: ['EV012', 'EV014'], fields: [['product_type', '产品类别', 'AI 行业信息平台'], ['core_function', '核心功能', '通过关注从业者及算法推荐获取 AI 内容'], ['differentiation', '差异化特点', '新技术、产品和观点通常先在 X 出现，再传导至其他平台']] },
  { key: 'K002', type: 'concept', title: '按需学习', core: '按需学习是先创造真实场景和需求，再在解决问题时补充所需知识的学习方式。', timeRange: '02:20.000-02:49.000', confidence: .98, evidenceIds: ['EV006'], fields: [['definition', '定义', '先创造真实场景和需求，再在解决问题时补充所需知识的学习方式'], ['mechanism', '运行机制', '真实任务先暴露知识缺口，学习者随后补充原理和架构知识'], ['distinction', '相邻区别', '学习顺序由“先理论、后实操”调整为“先实操、后理论”'], ['scope', '适用边界', '以学习和探索为目的的 AI 工具实践']] },
  { key: 'K003', type: 'methodology', title: '实践驱动的 AI 学习法', core: '该方法从个人真实需求出发，用 AI 完成小产品，并在实践中即时补充理论。', timeRange: '01:59.850-02:49.000', confidence: .97, evidenceIds: ['EV005', 'EV006'], fields: [['input', '输入', '个人工作中的效率需求、场景或问题'], ['steps', '步骤', '1. 选择一个能提升个人效率的小工具作为任务\n2. 直接让 AI 协助制作，在遇到不懂之处时即时追问\n3. 完成产品后，再追问项目架构和技术原理'], ['output', '输出', '可使用的小产品，以及对 AI Coding、架构和原理的理解'], ['applicability', '适用条件', '适用于把 AI 作为工具使用、以学习和探索为目标的人']] },
  { key: 'K004', type: 'methodology', title: '公开学习法', core: '公开学习法通过分享学习过程、接收反馈并强化身份认同，帮助学习者持续行动。', timeRange: '02:49.000-03:59.000', confidence: .98, evidenceIds: ['EV007', 'EV009'], fields: [['input', '输入', '学习过程、阶段成果和心得'], ['steps', '步骤', '1. 把学习过程和心得公开分享\n2. 接收外界反馈\n3. 用反馈和身份认同推动下一轮学习'], ['output', '输出', '受众与影响力、及时反馈和持续学习动力'], ['applicability', '适用条件', '适用于长期学习中缺少外部反馈的人']] },
  { key: 'K005', type: 'methodology', title: '顺藤摸瓜筛选 AI 信息源', core: '该方法从已知可靠从业者的关注网络出发，逐层筛选更多一手 AI 信息源并训练推荐算法。', timeRange: '05:39.000-06:49.000', confidence: .99, evidenceIds: ['EV013', 'EV014'], fields: [['input', '输入', '一个已知且可信的 AI 行业从业者账号'], ['steps', '步骤', '1. 查看该账号的关注列表\n2. 沿候选账号继续查看其关注网络\n3. 阅读个人简介并筛选实际在顶尖 AI 公司工作的人\n4. 关注并互动，让推荐算法逐步提供相似来源'], ['criteria', '判断标准', '个人简介能表明其在相关 AI 公司从事实务工作'], ['output', '输出', '一组可持续提供一手 AI 信息的账号及更聚焦的推荐信息流'], ['applicability', '适用条件', '适用于在 X 上追踪快速变化的 AI 技术、产品和观点']] },
  { key: 'K006', type: 'methodology', title: 'AI 导师协作学习法', core: '该方法让学习者把工作场景和痛点交给 AI，共同确定用途、完成工具并追问其原理。', timeRange: '07:55.000-08:55.873', confidence: .98, evidenceIds: ['EV018', 'EV019'], fields: [['input', '输入', '工作内容、日常场景、痛点和具体疑问'], ['steps', '步骤', '1. 用自然语言或语音详细描述工作场景和痛点\n2. 与 AI 共同梳理可解决的问题\n3. 协作完成工具，并在过程中询问如何改进沟通\n4. 让 AI 解释架构、生成课程或题目，并继续追问不理解之处'], ['output', '输出', '可用工具、项目解释、课程或练习，以及针对具体问题的回答'], ['applicability', '适用条件', '适用于知道工作问题但尚不清楚 AI 用法的学习者']] },
  { key: 'K007', type: 'case', title: '日本书店 AI 工具书讨论', core: '视频作者拍摄并发布日本书店销售 AI 工具书的见闻，帖子获得 60 多万浏览并引发对学习方式的讨论。', timeRange: '00:00.165-00:35.160', confidence: .99, evidenceIds: ['EV001', 'EV002'], fields: [['context', '背景', '视频作者在日本书店看到大量教授 AI 工具用法的书'], ['actors', '参与对象', '视频作者和帖子评论者'], ['actions', '行动', '视频作者拍照并发布到 Twitter，评论者分享教程书出版滞后的经历与看法'], ['outcome', '结果', '帖子获得 60 多万浏览并形成讨论'], ['retrospective', '复盘判断', 'AI 学习方式存在明显信息差，需要重新审视依赖书本和系统课程的观念']] },
  { key: 'K008', type: 'case', title: 'Photoshop 教程书出版滞后案例', core: '一名出版社从业者制作 Photoshop 教程书时，排版尚未完成，软件功能已经变化。', timeRange: '00:20.990-00:28.165', confidence: .98, evidenceIds: ['EV002'], fields: [['context', '背景', '一名帖子评论者曾在出版社参与制作 Photoshop 教程书'], ['actors', '参与对象', '出版社从业者'], ['actions', '行动', '制作并排版 Photoshop 教程书'], ['outcome', '结果', '排版尚未完成，Photoshop 功能已经变化']] },
  { key: 'K009', type: 'case', title: '发布 Vibe Coding 产品获得用户反馈', core: '视频作者发布 Vibe Coding 产品后，从用户问题和开发者反馈中了解了需求、痛点并补充了专业知识。', timeRange: '06:49.000-07:18.000', confidence: .98, evidenceIds: ['EV015'], fields: [['context', '背景', '视频作者通过输出内容和产品与网友、粉丝接触'], ['actors', '参与对象', '视频作者、产品用户和开发者粉丝'], ['actions', '行动', '发布 Vibe Coding 产品并接收反馈和问题'], ['outcome', '结果', '了解到用户关心的问题、实际需求和痛点，并从更专业的开发者处学到新知识'], ['retrospective', '复盘判断', '学习应发生在与外界的接触和碰撞中']] },
  { key: 'K010', type: 'insight', title: 'AI 工具学习应先实践再补理论', core: '对多数把 AI 当作工具的人，先完成真实任务再补充理论，比只靠书本学习更合适。', timeRange: '00:35.260-02:49.000', confidence: .99, evidenceIds: ['EV003', 'EV004', 'EV006'], fields: [['claim', '核心判断', '对多数把 AI 当作工具的人，应先完成真实任务，再按需补充理论'], ['reasoning', '推导依据', 'AI 工具需要动手使用才能掌握，且工具变化速度可能快于书籍出版周期'], ['qualifications', '限定条件', '不适用于视频中所说的 AI 研究者；书本仍适合学习相对稳定和经典的知识'], ['implications', '影响建议', '把学习顺序从“先理论、后实操”调整为“先实操、后理论”']] },
  { key: 'K011', type: 'insight', title: '行动与反馈会生成学习动力', core: '学习动力更多由行动后的及时反馈产生，而不是必须在行动前准备好。', timeRange: '02:49.000-03:59.000', confidence: .99, evidenceIds: ['EV007', 'EV008', 'EV009'], fields: [['claim', '核心判断', '学习动力更多由行动后的及时反馈产生，而不是必须在行动前准备好'], ['reasoning', '推导依据', '公开分享能带来外界反馈和身份认同，反馈推动继续行动，持续不行动则容易形成负循环'], ['qualifications', '限定条件', '该判断由作者一次缺乏动力并向 Claude 求助的个人经历引出'], ['implications', '影响建议', '缺少动力时可先采取一次可获得反馈的学习行动']] },
  { key: 'K012', type: 'insight', title: 'AI 信息源需要主动筛选', core: '在快速变化且内容嘈杂的 AI 领域，学习质量取决于能否主动筛选一手信息源并训练推荐信息流。', timeRange: '04:38.000-06:49.000', confidence: .98, evidenceIds: ['EV011', 'EV012', 'EV014'], fields: [['claim', '核心判断', 'AI 学习者需要主动筛选一手信息源并训练推荐信息流'], ['reasoning', '推导依据', '互联网虽有高质量内容但噪声大，而实务从业者能提供更新更快的一手经验'], ['qualifications', '限定条件', '获得高质量结果的前提是关注正确的人并持续调教算法'], ['implications', '影响建议', '优先关注实际从事产品、研究或业务的人，而不是被动接受默认推荐']] },
  { key: 'K013', type: 'insight', title: 'AI 可以成为低心理负担的全天候老师', core: 'AI 能以易懂语言持续回应基础问题，因此可作为学习 AI 工具时低心理负担的随时可用老师。', timeRange: '07:35.000-08:55.873', confidence: .98, evidenceIds: ['EV017', 'EV019'], fields: [['claim', '核心判断', 'AI 可以作为学习 AI 工具时低心理负担的全天候老师'], ['reasoning', '推导依据', 'AI 能用初学者易懂的语言耐心解释、持续在线，并允许学习者反复追问'], ['implications', '影响建议', '遇到工具用法或陌生内容时，可先用自然语言直接提问并继续追问']] },
  { key: 'K014', type: 'concept', title: 'AI 学习的三类老师', core: '视频把适合 AI 学习的老师归纳为稍微超前的同阶段学习者、顶尖实务从业者，以及网友和粉丝三类。', timeRange: '04:02.000-07:18.000', confidence: .98, evidenceIds: ['EV010', 'EV011', 'EV015'], fields: [['definition', '定义', '视频作者对 AI 学习中三类有效知识来源的归纳'], ['components', '构成要素', '1. 背景相近但阶段稍微超前的同行、同事或网友\n2. 顶尖行业从业者和实际做产品、研究或业务的 Builder\n3. 能提供需求反馈或专业补充的网友和粉丝'], ['mechanism', '运行机制', '三类老师分别提供易理解的阶段经验、一手行业经验和来自真实使用场景的反馈']] },
]

const relations: FixtureRelation[] = [
  { id: 'R001', source: 'K002', type: 'part_of', target: 'K003', evidenceIds: ['EV006'], confidence: .97 },
  { id: 'R002', source: 'K010', type: 'explains', target: 'K003', evidenceIds: ['EV004', 'EV006'], confidence: .96 },
  { id: 'R003', source: 'K003', type: 'complements', target: 'K004', evidenceIds: ['EV006', 'EV007'], confidence: .93 },
  { id: 'R004', source: 'K009', type: 'example_of', target: 'K004', evidenceIds: ['EV007', 'EV015'], confidence: .95 },
  { id: 'R005', source: 'K009', type: 'supports', target: 'K011', evidenceIds: ['EV007', 'EV009', 'EV015'], confidence: .94 },
  { id: 'R006', source: 'K011', type: 'explains', target: 'K004', evidenceIds: ['EV007', 'EV009'], confidence: .97 },
  { id: 'R007', source: 'K008', type: 'part_of', target: 'K007', evidenceIds: ['EV002'], confidence: .98 },
  { id: 'R008', source: 'K007', type: 'supports', target: 'K010', evidenceIds: ['EV001', 'EV002', 'EV003'], confidence: .94 },
  { id: 'R009', source: 'K008', type: 'supports', target: 'K010', evidenceIds: ['EV002', 'EV003'], confidence: .98 },
  { id: 'R010', source: 'K005', type: 'applies_to', target: 'K001', evidenceIds: ['EV013', 'EV014'], confidence: .99 },
  { id: 'R011', source: 'K012', type: 'explains', target: 'K005', evidenceIds: ['EV012', 'EV013'], confidence: .97 },
  { id: 'R012', source: 'K013', type: 'explains', target: 'K006', evidenceIds: ['EV017', 'EV018', 'EV019'], confidence: .98 },
  { id: 'R013', source: 'K009', type: 'example_of', target: 'K014', evidenceIds: ['EV015'], confidence: .94 },
  { id: 'R014', source: 'K014', type: 'complements', target: 'K013', evidenceIds: ['EV010', 'EV015', 'EV017'], confidence: .91 },
  { id: 'R015', source: 'K007', type: 'involves', target: 'K001', evidenceIds: ['EV001'], confidence: .99 },
]

const typeLabels: Record<KnowledgeType, string> = { entity: '实体', concept: '概念', methodology: '方法论', case: '案例', insight: '洞察' }
const informationNature: Record<KnowledgeType, string> = { entity: '产品', concept: '概念', methodology: '方法论', case: '案例', insight: '洞察' }
const objectByKey = new Map(objects.map(item => [item.key, item]))
const evidenceById = new Map(evidence.map(item => [item.id, item]))

function pageId(key: string): string {
  return `00000000-0000-4000-8000-${key.slice(1).padStart(12, '0')}`
}

function slug(item: FixtureObject): string {
  return `knowledge-object/${item.type}/${item.key.toLowerCase()}`
}

function startSeconds(item: FixtureObject): number {
  const first = evidenceById.get(item.evidenceIds[0])
  return first ? first.startMs / 1000 : 0
}

function formatTime(seconds: number): string {
  const value = Math.max(0, Math.floor(seconds))
  return `${String(Math.floor(value / 60)).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}`
}

function structureFields(item: FixtureObject): KnowledgeStructureField[] {
  return item.fields.map(([key, label, value]) => ({ key, label, value }))
}

function graphEvidence(item: FixtureObject): GraphEvidence[] {
  return item.evidenceIds.flatMap((id) => {
    const value = evidenceById.get(id)
    if (!value) return []
    return [{
      video_id: AI_LEARNING_EVAL_VIDEO_ID,
      video_title: VIDEO_TITLE,
      start_ms: value.startMs,
      end_ms: value.endMs,
      chunk_index: Number(id.slice(2)) - 1,
      transcript_generation: TRANSCRIPT_GENERATION,
      evidence_sentence_id: id,
      text: value.text,
    }]
  })
}

function detail(item: FixtureObject): GraphKnowledgeDetail {
  return {
    id: pageId(item.key),
    knowledge_object_id: `LOCAL-EVAL-AI-LEARNING-${item.key}`,
    slug: slug(item),
    title: item.title,
    video_id: AI_LEARNING_EVAL_VIDEO_ID,
    video_title: VIDEO_TITLE,
    source_video_title: VIDEO_TITLE,
    timestamp: formatTime(startSeconds(item)),
    seconds: startSeconds(item),
    knowledge_type: item.type,
    primary_type: item.type,
    audit_status: 'passed',
    transcript_generation: TRANSCRIPT_GENERATION,
    classification_confidence: item.confidence,
    entity_sub_type: item.entitySubType,
    page_type: 'index',
    core_content: item.core,
    structure_fields: structureFields(item),
    evidence_ids: item.evidenceIds,
    information_nature: informationNature[item.type],
    time_range: item.timeRange,
    related_content: [],
    relations: [],
  }
}

const allDetails = objects.map(detail)
const detailsById = new Map(allDetails.map(item => [item.id, item]))
const detailsByKey = new Map(objects.map((item, index) => [item.key, allDetails[index]]))

const allEdges: GraphEdge[] = relations.map(relation => ({
  id: `LOCAL-EVAL-AI-LEARNING-${relation.id}`,
  source: pageId(relation.source),
  target: pageId(relation.target),
  type: relation.type,
  confidence: relation.confidence,
  evidence_ids: relation.evidenceIds,
  relation_kind: 'semantic',
  relation_source: 'skill',
  counted: true,
}))

export function isAiLearningEvalFixtureEnabled(): boolean {
  if (typeof window === 'undefined') return false
  return new URLSearchParams(window.location.search).get('fixture') === AI_LEARNING_EVAL_FIXTURE
}

export function getAiLearningEvalGraph(req: WikiGraphRequest = {}): KnowledgeGraphPayload {
  const allowed = new Set(req.types || [])
  const filteredDetails = allowed.size ? allDetails.filter(item => allowed.has(item.knowledge_type)) : allDetails
  const limitedDetails = filteredDetails.slice(0, req.limit || filteredDetails.length)
  const visible = new Set(limitedDetails.map(item => item.id))
  const filteredEdges = allEdges.filter(edge => visible.has(edge.source) && visible.has(edge.target))
  const degree = new Map<string, number>()
  allEdges.forEach(edge => {
    degree.set(edge.source, (degree.get(edge.source) || 0) + 1)
    degree.set(edge.target, (degree.get(edge.target) || 0) + 1)
  })
  const nodes = limitedDetails.map(item => ({
    id: item.id,
    name: item.title,
    label: item.title,
    attributes: [typeLabels[item.knowledge_type]],
    type: typeLabels[item.knowledge_type],
    knowledge_type: item.knowledge_type,
    wiki_page_id: item.id,
    knowledge_object_id: item.knowledge_object_id,
    video_id: AI_LEARNING_EVAL_VIDEO_ID,
    video_title: VIDEO_TITLE,
    seconds: item.seconds,
    link_count: degree.get(item.id) || 0,
    is_orphan: false,
    audit_status: 'passed',
    knowledge_detail: item,
  }))
  const typeCounts = Object.fromEntries(Object.keys(typeLabels).map(type => [type, allDetails.filter(item => item.knowledge_type === type).length]))
  return {
    status: nodes.length ? 'ready' : 'filter_empty',
    knowledge_base_id: 'local-eval-ai-learning',
    nodes,
    edges: filteredEdges,
    reading_associations: [],
    wiki_pages: limitedDetails,
    meta: { mode: req.mode || 'overview', total: filteredDetails.length, returned: nodes.length, truncated: nodes.length < filteredDetails.length, semantic_edge_count: filteredEdges.length, reading_association_count: 0 },
    attributes: Object.values(typeLabels),
    counts: {
      scope_nodes: 14,
      filtered_nodes: filteredDetails.length,
      candidate_nodes: filteredDetails.length,
      returned_nodes: nodes.length,
      type_counts: typeCounts,
      type_denominator: 14,
      unknown_types: 0,
      unknown_type_denominator: 14,
      formal_relations: filteredEdges.length,
      formal_relation_denominator: 15,
      reading_associations: 0,
      reading_association_denominator: 0,
      orphan_nodes: 0,
      orphan_denominator: 14,
      rejected_records: 0,
      rejected_denominator: 14,
    },
  }
}

export function getAiLearningEvalDetail(wikiPageId: string): KnowledgeGraphDetailPayload {
  const value = detailsById.get(wikiPageId)
  if (!value) throw new Error('离线测评中不存在该 Wiki 页面')
  const item = objectByKey.get(value.knowledge_object_id?.slice(-4) || '')
  if (!item) throw new Error('离线测评对象映射不完整')
  const formalRelations = allEdges.filter(edge => edge.source === wikiPageId || edge.target === wikiPageId)
  return {
    status: 'ready',
    knowledge_base_id: 'local-eval-ai-learning',
    detail: value,
    evidence: graphEvidence(item),
    formal_relations: formalRelations,
    reading_associations: [],
    counts: {
      formal_relations: formalRelations.length,
      formal_relation_denominator: formalRelations.length,
      reading_associations: 0,
      reading_association_denominator: 0,
      evidence: item.evidenceIds.length,
      evidence_denominator: item.evidenceIds.length,
    },
  }
}

export function getAiLearningEvalRelatedKnowledge(videoId: string): RelatedKnowledgePayload {
  const anchors = objects.map((item) => {
    const incident = relations.filter(relation => relation.source === item.key || relation.target === item.key)
    return {
      id: pageId(item.key),
      knowledge_type: item.type,
      content: item.title,
      coreContent: item.core,
      structureFields: structureFields(item),
      informationNature: informationNature[item.type],
      timeRange: item.timeRange,
      sourceVideoTitle: VIDEO_TITLE,
      relatedContent: [],
      relations: incident.map((relation) => {
        const targetKey = relation.source === item.key ? relation.target : relation.source
        const target = objectByKey.get(targetKey)!
        return { id: relation.id, relationType: relation.type, targetId: pageId(targetKey), targetTitle: target.title, targetType: target.type, confidence: relation.confidence }
      }),
      evidence: graphEvidence(item).map(value => ({ id: value.evidence_sentence_id || '', text: value.text || '', timestamp: `${formatTime(value.start_ms / 1000)}-${formatTime(value.end_ms / 1000)}`, seconds: value.start_ms / 1000 })),
      timestamp: formatTime(startSeconds(item)),
      seconds: startSeconds(item),
      related_count: incident.length,
    }
  })
  return {
    videoId,
    overview: {
      relation_overview: '14 个知识对象已组成同视频知识网络；页面颗粒度保持不变，通过关系和证据形成连续阅读路径。',
      related_video_count: 0,
      relation_count: 15,
      top_topics: ['实践驱动学习', '公开学习', '信息源筛选', 'AI 导师'],
    },
    anchors,
    crossVideoItems: [],
  }
}

export function getAiLearningEvalCrossVideo(): CrossVideoPayload {
  return {
    status: 'empty',
    video_id: AI_LEARNING_EVAL_VIDEO_ID,
    associations: [],
    rejected: [],
    candidate_count: 0,
    current_page_count: 14,
    other_video_count: 0,
  }
}

export function findAiLearningEvalPageBySlug(pageSlug: string): GraphKnowledgeDetail | undefined {
  return allDetails.find(item => item.slug === pageSlug)
}

export function getAiLearningEvalTitle(pageIdValue: string): string {
  return detailsById.get(pageIdValue)?.title || detailsByKey.get(pageIdValue)?.title || ''
}
