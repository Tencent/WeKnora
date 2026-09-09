import type { GlobalConfigProvider } from 'tdesign-vue-next'
import 'dayjs/locale/fr.js'

// TDesign 1.19 does not ship a French locale. Keep this configuration local
// until an upstream French bundle can replace it without changing callers.
const frFR = {
  autoComplete: { empty: 'Aucune donnée' },
  pagination: {
    itemsPerPage: '{size} / page',
    jumpTo: 'Aller à',
    page: 'page',
    total: 'Aucun élément | 1 élément | {count} éléments',
  },
  cascader: { empty: 'Aucune donnée', loadingText: 'Chargement…', placeholder: 'Sélectionner' },
  calendar: {
    yearSelection: '{year}', monthSelection: '{month}',
    yearRadio: 'Année', monthRadio: 'Mois',
    hideWeekend: 'Masquer le week-end', showWeekend: 'Afficher le week-end',
    today: "Aujourd’hui", thisMonth: 'Ce mois-ci', firstDayOfWeek: 1,
    week: 'Lundi,Mardi,Mercredi,Jeudi,Vendredi,Samedi,Dimanche',
    cellMonth: 'Janvier,Février,Mars,Avril,Mai,Juin,Juillet,Août,Septembre,Octobre,Novembre,Décembre',
  },
  transfer: { title: '{checked} / {total}', empty: 'Aucune donnée', placeholder: 'Rechercher' },
  timePicker: {
    dayjsLocale: 'fr', now: 'Maintenant', confirm: 'Confirmer',
    anteMeridiem: 'AM', postMeridiem: 'PM', placeholder: 'Sélectionner',
  },
  dialog: { confirm: 'Confirmer', cancel: 'Annuler' },
  drawer: { confirm: 'Confirmer', cancel: 'Annuler' },
  popconfirm: { confirm: { content: 'Confirmer' }, cancel: { content: 'Annuler' } },
  table: {
    empty: 'Aucune donnée', loadingText: 'Chargement…', loadingMoreText: 'Chargement de la suite…',
    filterInputPlaceholder: '',
    sortAscendingOperationText: 'Trier par ordre croissant',
    sortCancelOperationText: 'Annuler le tri',
    sortDescendingOperationText: 'Trier par ordre décroissant',
    clearFilterResultButtonText: 'Effacer', columnConfigButtonText: 'Colonnes',
    columnConfigTitleText: 'Configuration des colonnes',
    columnConfigDescriptionText: 'Sélectionnez les colonnes à afficher dans le tableau',
    confirmText: 'Confirmer', cancelText: 'Annuler', resetText: 'Réinitialiser',
    selectAllText: 'Tout sélectionner',
    searchResultText: 'Recherche « {result} » : aucun résultat | Recherche « {result} » : 1 résultat | Recherche « {result} » : {count} résultats',
  },
  select: { empty: 'Aucune donnée', loadingText: 'Chargement…', placeholder: 'Sélectionner' },
  tree: { empty: 'Aucune donnée' },
  treeSelect: { empty: 'Aucune donnée', loadingText: 'Chargement…', placeholder: 'Sélectionner' },
  datePicker: {
    dayjsLocale: 'fr', firstDayOfWeek: 1,
    placeholder: {
      date: 'Sélectionner une date', month: 'Sélectionner un mois', year: 'Sélectionner une année',
      quarter: 'Sélectionner un trimestre', week: 'Sélectionner une semaine',
    },
    weekdays: ['Lun', 'Mar', 'Mer', 'Jeu', 'Ven', 'Sam', 'Dim'],
    months: ['Janv.', 'Févr.', 'Mars', 'Avr.', 'Mai', 'Juin', 'Juil.', 'Août', 'Sept.', 'Oct.', 'Nov.', 'Déc.'],
    quarters: ['T1', 'T2', 'T3', 'T4'], rangeSeparator: ' – ', direction: 'ltr', format: 'DD/MM/YYYY',
    dayAriaLabel: 'Jour', yearAriaLabel: 'Année', monthAriaLabel: 'Mois', weekAbbreviation: 'Sem.',
    confirm: 'Confirmer', selectTime: 'Sélectionner une heure', selectDate: 'Sélectionner une date',
    nextYear: 'Année suivante', preYear: 'Année précédente', nextMonth: 'Mois suivant', preMonth: 'Mois précédent',
    preDecade: 'Décennie précédente', nextDecade: 'Décennie suivante', now: 'Maintenant',
  },
  upload: {
    sizeLimitMessage: 'Le fichier dépasse la taille maximale autorisée. {sizeLimit}',
    cancelUploadText: 'Annuler',
    triggerUploadText: {
      fileInput: 'Importer', image: 'Cliquer pour importer', normal: 'Importer', reupload: 'Réimporter',
      continueUpload: 'Continuer l’importation', delete: 'Supprimer', uploading: 'Importation en cours',
    },
    dragger: {
      dragDropText: 'Déposer ici', draggingText: 'Glissez le fichier dans cette zone pour l’importer',
      clickAndDragText: 'Cliquez sur « Importer » ou glissez un fichier dans cette zone',
    },
    file: {
      fileNameText: 'Nom', fileSizeText: 'Taille', fileStatusText: 'État',
      fileOperationText: 'Action', fileOperationDateText: 'Date',
    },
    progress: {
      uploadingText: 'Importation en cours', waitingText: 'En attente', failText: 'Échec', successText: 'Terminé',
    },
  },
  form: {
    errorMessage: {
      date: '${name} n’est pas valide', url: '${name} n’est pas valide',
      required: '${name} est obligatoire', whitespace: '${name} ne peut pas être vide',
      max: '${name} ne peut pas dépasser ${validate} caractères',
      min: '${name} doit contenir au moins ${validate} caractères',
      len: '${name} doit contenir exactement ${validate} caractères',
      enum: '${name} doit faire partie des valeurs suivantes : ${validate}',
      idcard: '${name} n’est pas valide', telnumber: '${name} n’est pas valide',
      pattern: '${name} n’est pas valide', validator: '${name} n’est pas valide',
      boolean: '${name} doit être un booléen', number: '${name} doit être un nombre',
      email: '${name} n’est pas valide',
    },
    colonText: ' :',
  },
  input: { placeholder: 'Saisir une valeur' },
  list: { loadingText: 'Chargement…', loadingMoreText: 'Chargement de la suite…' },
  alert: { expandText: 'Développer', collapseText: 'Réduire' },
  anchor: { copySuccessText: 'Lien copié', copyText: 'Copier le lien' },
  colorPicker: {
    swatchColorTitle: 'Couleurs par défaut', recentColorTitle: 'Couleurs récentes',
    clearConfirmText: 'Effacer les couleurs récentes ?', singleColor: 'Uni', gradientColor: 'Dégradé',
  },
  guide: {
    finishButtonProps: { content: 'Terminer', theme: 'primary' as const },
    nextButtonProps: { content: 'Suivant', theme: 'primary' as const },
    skipButtonProps: { content: 'Ignorer', theme: 'default' as const },
    prevButtonProps: { content: 'Précédent', theme: 'default' as const },
  },
  image: { errorText: 'Impossible de charger l’image', loadingText: 'Chargement…' },
  imageViewer: {
    errorText: 'Impossible de charger l’image', mirrorTipText: 'Miroir', rotateTipText: 'Pivoter',
    originalSizeTipText: 'Taille originale', previewText: 'Aperçu',
  },
  typography: { expandText: 'Afficher plus', collapseText: 'Réduire', copiedText: 'Copié' },
  rate: { rateText: ['Très insatisfait', 'Insatisfait', 'Neutre', 'Satisfait', 'Très satisfait'] },
  empty: { titleText: {
    maintenance: 'En maintenance', success: 'Succès', fail: 'Échec', empty: 'Aucune donnée', networkError: 'Erreur réseau',
  } },
  descriptions: { colonText: ' :' },
  chat: {
    placeholder: 'Saisissez votre message…', stopBtnText: 'Arrêter', refreshTipText: 'Régénérer',
    copyTipText: 'Copier', likeTipText: 'J’aime', dislikeTipText: 'Je n’aime pas', shareTipText: 'Partager',
    copyCodeBtnText: 'Copier le code', copyCodeSuccessText: 'Code copié',
    clearHistoryBtnText: 'Effacer l’historique', copyTextSuccess: 'Copié', copyTextFail: 'Échec de la copie',
    confirmClearHistory: 'Voulez-vous effacer tous les messages ?',
    loadingText: 'Réflexion en cours…', loadingEndText: 'Réflexion terminée',
    uploadImageText: 'Importer une image', uploadAttachmentText: 'Joindre un fichier',
  },
  qrcode: { expiredText: 'Expiré', refreshText: 'Actualiser', scannedText: 'Scanné' },
}

// Assign after inference: some labels supported at runtime are absent from
// TDesign's public interface (e.g. quarter/week date placeholders).
const config: GlobalConfigProvider = frFR
export default config
