./whisper.cpp/build/bin/whisper-cli \
  -m whisper.cpp/models/ggml-large-v3.bin -l ko \
  --max-context 0 \
  --no-speech-thold 0.01 \
  --suppress-nst \
  --no-prints \
  --vad \
  --vad-model whisper.cpp/models/ggml-silero-v5.1.2.bin \
  --vad-threshold 0.01 \
  --output-lrc sample/test_stt.wav


# 사용법: ./build/bin/whisper-cli [옵션] file0 file1 ...
# 지원 오디오 포맷: flac, mp3, ogg, wav

# 옵션:
#   -h,        --help              [기본값] 도움말 메시지 출력 후 종료
#   -t N,      --threads N         [4      ] 계산에 사용할 스레드 수
#   -p N,      --processors N      [1      ] 계산에 사용할 프로세서 수
#   -ot N,     --offset-t N        [0      ] 오디오 시작 오프셋(ms)
#   -on N,     --offset-n N        [0      ] 세그먼트 인덱스 오프셋
#   -d  N,     --duration N        [0      ] 처리할 오디오 길이(ms)
#   -mc N,     --max-context N     [-1     ] 저장할 최대 텍스트 컨텍스트 토큰 수
#   -ml N,     --max-len N         [0      ] 세그먼트 최대 길이(문자 수)
#   -sow,      --split-on-word     [false  ] 토큰이 아닌 단어 기준으로 분할
#   -bo N,     --best-of N         [5      ] 유지할 최상 후보 수
#   -bs N,     --beam-size N       [5      ] 빔 서치의 빔 크기
#   -ac N,     --audio-ctx N       [0      ] 오디오 컨텍스트 크기 (0 - 전체)
#   -wt N,     --word-thold N      [0.01   ] 단어 타임스탬프 확률 임계치
#   -et N,     --entropy-thold N   [2.40   ] 디코더 실패 엔트로피 임계치
#   -lpt N,    --logprob-thold N   [-1.00  ] 디코더 실패 로그 확률 임계치
#   -nth N,    --no-speech-thold N [0.60   ] 무음(no-speech) 임계치
#   -tp,       --temperature N     [0.00   ] 샘플링 온도(0~1)
#   -tpi,      --temperature-inc N [0.20   ] 온도 증가값(0~1)
#   -debug,    --debug-mode        [false  ] 디버그 모드 활성화(예: log_mel 덤프)
#   -tr,       --translate         [false  ] 원본 언어에서 영어로 번역
#   -di,       --diarize           [false  ] 스테레오 오디오 화자 분리
#   -tdrz,     --tinydiarize       [false  ] tinydiarize 활성화(tdraz 모델 필요)
#   -nf,       --no-fallback       [false  ] 디코딩 시 온도 fallback 사용 안 함
#   -otxt,     --output-txt        [false  ] 결과를 텍스트 파일로 출력
#   -ovtt,     --output-vtt        [false  ] 결과를 vtt 파일로 출력
#   -osrt,     --output-srt        [false  ] 결과를 srt 파일로 출력
#   -olrc,     --output-lrc        [false  ] 결과를 lrc 파일로 출력
#   -owts,     --output-words      [false  ] 노래방 영상 생성 스크립트 출력
#   -fp,       --font-path         [/System/Library/Fonts/Supplemental/Courier New Bold.ttf] 노래방 영상용 고정폭 폰트 경로
#   -ocsv,     --output-csv        [false  ] 결과를 CSV 파일로 출력
#   -oj,       --output-json       [false  ] 결과를 JSON 파일로 출력
#   -ojf,      --output-json-full  [false  ] JSON 파일에 추가 정보 포함
#   -of FNAME, --output-file FNAME [       ] 출력 파일 경로(확장자 제외)
#   -np,       --no-prints         [false  ] 결과 외 모든 출력 생략
#   -ps,       --print-special     [false  ] 특수 토큰 출력
#   -pc,       --print-colors      [false  ] 색상 출력
#              --print-confidence  [false  ] 신뢰도 출력
#   -pp,       --print-progress    [false  ] 진행 상황 출력
#   -nt,       --no-timestamps     [false  ] 타임스탬프 출력 안 함
#   -l LANG,   --language LANG     [en     ] 언어(‘auto’ 자동 감지)
#   -dl,       --detect-language   [false  ] 자동 언어 감지 후 종료
#              --prompt PROMPT     [       ] 초기 프롬프트(n_text_ctx/2 토큰까지)
#   -m FNAME,  --model FNAME       [models/ggml-base.en.bin] 모델 경로
#   -f FNAME,  --file FNAME        [       ] 입력 오디오 파일 경로
#   -oved D,   --ov-e-device DNAME [CPU    ] OpenVINO 인코딩 추론 디바이스
#   -dtw MODEL --dtw MODEL         [       ] 토큰 단위 타임스탬프 계산
#   -ls,       --log-score         [false  ] 토큰의 최고 디코더 점수 로그
#   -ng,       --no-gpu            [false  ] GPU 사용 안 함
#   -fa,       --flash-attn        [false  ] 플래시 어텐션
#   -sns,      --suppress-nst      [true   ] 비음성 토큰 억제
#   --suppress-regex REGEX         [       ] 억제할 토큰 정규식
#   --grammar GRAMMAR              [       ] 디코딩 가이드용 GBNF 문법
#   --grammar-rule RULE            [       ] 최상위 GBNF 문법 규칙명
#   --grammar-penalty N            [100.0  ] 비문법 토큰 로짓 감소 배율

# 음성 활동 감지(VAD) 옵션:
#              --vad                           [true   ] 음성 활동 감지(VAD) 활성화
#   -vm FNAME, --vad-model FNAME               [models/ggml-silero-v5.1.2.bin] VAD 모델 경로
#   -vt N,     --vad-threshold N               [0.50   ] 음성 인식용 VAD 임계치
#   -vspd N,   --vad-min-speech-duration-ms  N [250    ] 최소 발화 길이(ms)
#   -vsd N,    --vad-min-silence-duration-ms N [100    ] 세그먼트 분할용 최소 무음 길이(ms)
#   -vmsd N,   --vad-max-speech-duration-s   N [FLT_MAX] 최대 발화 길이(자동 분할)
#   -vp N,     --vad-speech-pad-ms           N [30     ] 발화 패딩(ms, 구간 확장)
#   -vo N,     --vad-samples-overlap         N [0.10   ] 세그먼트 간 오버랩(초)