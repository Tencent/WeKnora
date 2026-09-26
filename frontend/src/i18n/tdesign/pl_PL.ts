// Polish locale for TDesign components (TDesign 1.19 ships no pl_PL).
// Built as a deep override of en_US so keys added upstream fall back to English.
import 'dayjs/locale/pl'
import enUSConfig from 'tdesign-vue-next/esm/locale/en_US'

type Tree = Record<string, unknown>

function deepMerge(base: Tree, patch: Tree): Tree {
  const out: Tree = { ...base }
  for (const [key, value] of Object.entries(patch)) {
    const current = out[key]
    out[key] =
      value && typeof value === 'object' && !Array.isArray(value) && current && typeof current === 'object' && !Array.isArray(current)
        ? deepMerge(current as Tree, value as Tree)
        : value
  }
  return out
}

const plPL: Tree = {
  autoComplete: { empty: 'Brak danych' },
  pagination: {
    itemsPerPage: '{size} / stronę',
    jumpTo: 'Przejdź do',
    page: '',
    total: 'Łącznie: 0 | Łącznie: 1 | Łącznie: {count}',
  },
  cascader: { empty: 'Brak danych', loadingText: 'Ładowanie…', placeholder: 'Wybierz' },
  calendar: {
    yearSelection: '{year}',
    monthSelection: '{month}',
    yearRadio: 'rok',
    monthRadio: 'miesiąc',
    hideWeekend: 'Ukryj weekend',
    showWeekend: 'Pokaż weekend',
    today: 'Dzisiaj',
    thisMonth: 'Ten miesiąc',
    week: 'Poniedziałek,Wtorek,Środa,Czwartek,Piątek,Sobota,Niedziela',
    cellMonth: 'Styczeń,Luty,Marzec,Kwiecień,Maj,Czerwiec,Lipiec,Sierpień,Wrzesień,Październik,Listopad,Grudzień',
  },
  transfer: { title: '{checked} / {total}', empty: 'Brak danych', placeholder: 'Wpisz szukaną frazę' },
  timePicker: {
    dayjsLocale: 'pl',
    now: 'Teraz',
    confirm: 'Potwierdź',
    anteMeridiem: 'AM',
    postMeridiem: 'PM',
    placeholder: 'Wybierz godzinę',
  },
  dialog: { confirm: 'Potwierdź', cancel: 'Anuluj' },
  drawer: { confirm: 'Potwierdź', cancel: 'Anuluj' },
  popconfirm: { confirm: { content: 'OK' }, cancel: { content: 'Anuluj' } },
  table: {
    empty: 'Brak danych',
    loadingText: 'Ładowanie…',
    loadingMoreText: 'Ładowanie kolejnych…',
    filterInputPlaceholder: '',
    sortAscendingOperationText: 'Kliknij, aby sortować rosnąco',
    sortCancelOperationText: 'Kliknij, aby wyłączyć sortowanie',
    sortDescendingOperationText: 'Kliknij, aby sortować malejąco',
    clearFilterResultButtonText: 'Wyczyść',
    columnConfigButtonText: 'Kolumny',
    columnConfigTitleText: 'Konfiguracja kolumn tabeli',
    columnConfigDescriptionText: 'Wybierz kolumny wyświetlane w tabeli',
    confirmText: 'Potwierdź',
    cancelText: 'Anuluj',
    resetText: 'Resetuj',
    selectAllText: 'Zaznacz wszystko',
    searchResultText:
      'Wyszukiwanie „{result}”: brak wyników. | Wyszukiwanie „{result}”: wyniki: 1. | Wyszukiwanie „{result}”: wyniki: {count}.',
  },
  select: { empty: 'Brak danych', loadingText: 'Ładowanie…', placeholder: 'Wybierz' },
  tree: { empty: 'Brak danych' },
  treeSelect: { empty: 'Brak danych', loadingText: 'Ładowanie…', placeholder: 'Wybierz' },
  datePicker: {
    dayjsLocale: 'pl',
    placeholder: {
      date: 'Wybierz datę',
      month: 'Wybierz miesiąc',
      year: 'Wybierz rok',
      quarter: 'Wybierz kwartał',
      week: 'Wybierz tydzień',
    },
    weekdays: ['Pn', 'Wt', 'Śr', 'Cz', 'Pt', 'So', 'Nd'],
    months: ['Sty', 'Lut', 'Mar', 'Kwi', 'Maj', 'Cze', 'Lip', 'Sie', 'Wrz', 'Paź', 'Lis', 'Gru'],
    quarters: ['I kw.', 'II kw.', 'III kw.', 'IV kw.'],
    rangeSeparator: ' – ',
    direction: 'ltr',
    format: 'YYYY-MM-DD',
    dayAriaLabel: 'D',
    yearAriaLabel: 'R',
    monthAriaLabel: 'M',
    weekAbbreviation: 'Tydz.',
    confirm: 'Potwierdź',
    selectTime: 'Wybierz godzinę',
    selectDate: 'Wybierz datę',
    nextYear: 'Następny rok',
    preYear: 'Poprzedni rok',
    nextMonth: 'Następny miesiąc',
    preMonth: 'Poprzedni miesiąc',
    preDecade: 'Poprzednia dekada',
    nextDecade: 'Następna dekada',
    now: 'Teraz',
  },
  upload: {
    sizeLimitMessage: 'Plik jest za duży. {sizeLimit}',
    cancelUploadText: 'Anuluj',
    triggerUploadText: {
      fileInput: 'Prześlij',
      image: 'Kliknij, aby przesłać',
      normal: 'Prześlij',
      reupload: 'Prześlij ponownie',
      continueUpload: 'Prześlij kolejne',
      delete: 'Usuń',
      uploading: 'Przesyłanie',
    },
    dragger: {
      dragDropText: 'Upuść tutaj',
      draggingText: 'Przeciągnij plik tutaj, aby go przesłać',
      clickAndDragText: 'Kliknij „Prześlij” albo przeciągnij plik tutaj',
    },
    file: {
      fileNameText: 'Nazwa pliku',
      fileSizeText: 'Rozmiar',
      fileStatusText: 'Status',
      fileOperationText: 'Operacje',
      fileOperationDateText: 'Data',
    },
    progress: { uploadingText: 'Przesyłanie', waitingText: 'Oczekuje', failText: 'Błąd', successText: 'Gotowe' },
  },
  form: {
    errorMessage: {
      date: 'Pole ${name} jest nieprawidłowe',
      url: 'Pole ${name} jest nieprawidłowe',
      required: 'Pole ${name} jest wymagane',
      whitespace: 'Pole ${name} nie może być puste',
      max: 'Pole ${name} może mieć najwyżej ${validate} znaków',
      min: 'Pole ${name} musi mieć co najmniej ${validate} znaków',
      len: 'Pole ${name} musi mieć dokładnie ${validate} znaków',
      enum: 'Pole ${name} musi mieć jedną z wartości: ${validate}',
      idcard: 'Pole ${name} jest nieprawidłowe',
      telnumber: 'Pole ${name} jest nieprawidłowe',
      pattern: 'Pole ${name} jest nieprawidłowe',
      validator: 'Pole ${name} jest nieprawidłowe',
      boolean: 'Pole ${name} musi mieć wartość logiczną',
      number: 'Pole ${name} musi być liczbą',
      email: 'Pole ${name} jest nieprawidłowe',
    },
    colonText: ':',
  },
  input: { placeholder: 'Wpisz' },
  list: { loadingText: 'Ładowanie…', loadingMoreText: 'Ładowanie kolejnych…' },
  alert: { expandText: 'Rozwiń', collapseText: 'Zwiń' },
  anchor: { copySuccessText: 'Skopiowano link', copyText: 'Kopiuj link' },
  colorPicker: {
    swatchColorTitle: 'Domyślne systemowe',
    recentColorTitle: 'Ostatnio używane',
    clearConfirmText: 'Wyczyścić ostatnio używane kolory?',
    singleColor: 'Jednolity',
    gradientColor: 'Gradient',
  },
  guide: {
    finishButtonProps: { content: 'Zakończ', theme: 'primary' },
    nextButtonProps: { content: 'Dalej', theme: 'primary' },
    skipButtonProps: { content: 'Pomiń', theme: 'default' },
    prevButtonProps: { content: 'Wstecz', theme: 'default' },
  },
  image: { errorText: 'Nie udało się wczytać', loadingText: 'Ładowanie' },
  imageViewer: {
    errorText: 'Nie udało się wczytać',
    mirrorTipText: 'Odbicie lustrzane',
    rotateTipText: 'Obróć',
    originalSizeTipText: 'Oryginalny rozmiar',
    previewText: 'Podgląd',
  },
  typography: { expandText: 'Więcej', collapseText: 'Zwiń', copiedText: 'Skopiowano' },
  rate: { rateText: ['fatalnie', 'rozczarowująco', 'przeciętnie', 'dobrze', 'znakomicie'] },
  empty: {
    titleText: {
      maintenance: 'Prace serwisowe',
      success: 'Gotowe',
      fail: 'Błąd',
      empty: 'Brak danych',
      networkError: 'Błąd sieci',
    },
  },
  descriptions: { colonText: ':' },
  chat: {
    placeholder: 'Wpisz wiadomość…',
    stopBtnText: 'Zatrzymaj',
    refreshTipText: 'Wygeneruj ponownie',
    copyTipText: 'Kopiuj',
    likeTipText: 'Przydatne',
    dislikeTipText: 'Nieprzydatne',
    copyCodeBtnText: 'Kopiuj kod',
    copyCodeSuccessText: 'Skopiowano',
    clearHistoryBtnText: 'Wyczyść historię',
    copyTextSuccess: 'Skopiowano',
    copyTextFail: 'Nie udało się skopiować',
    confirmClearHistory: 'Czy na pewno wyczyścić wszystkie wiadomości?',
    loadingText: 'Myślę…',
    loadingEndText: 'Zakończono rozważanie',
    uploadImageText: 'Prześlij obraz',
    uploadAttachmentText: 'Prześlij załącznik',
    shareTipText: 'Udostępnij',
  },
  qrcode: { expiredText: 'Wygasł', refreshText: 'Odśwież', scannedText: 'Zeskanowano' },
}

export default deepMerge(enUSConfig as unknown as Tree, plPL)
