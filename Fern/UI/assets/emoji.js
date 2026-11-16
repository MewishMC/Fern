/* Emoji, symbols and kaomoji for the panel's picker. Bundled with Fern so the
   picker works offline. Emoji and symbol entries are "<char> <search words>",
   one per line; kaomoji are [text, search words] pairs since they contain
   spaces. The words are what the search box matches. */
(function () {
    "use strict";

    const emoji = [
        {
            id: "smileys", name: "Smileys", icon: "fa-face-smile", items: `
😀 grinning face smile happy
😃 grinning big eyes smile happy
😄 grinning smiling eyes happy joy
😁 beaming grin teeth
😆 laughing squinting haha lol
😅 sweat smile nervous relief
🤣 rolling floor laughing rofl lol
😂 tears of joy laughing crying lol
🙂 slightly smiling face
🙃 upside down silly sarcasm
🫠 melting face embarrassed
😉 winking wink
😊 smiling blush happy
😇 halo angel innocent
🥰 smiling hearts love adore
😍 heart eyes love crush
🤩 star struck excited wow
😘 blowing kiss love
😗 kissing face
☺️ smiling relaxed
😚 kissing closed eyes
😙 kissing smiling eyes
🥲 smiling tear grateful touched
😋 yum delicious tongue
😛 tongue out
😜 winking tongue crazy
🤪 zany crazy goofy
😝 squinting tongue
🤑 money mouth rich
🤗 hugging hug
🤭 hand over mouth giggle oops
🫢 open eyes hand over mouth gasp
🫣 peeking eye shy
🤫 shushing quiet secret
🤔 thinking hmm
🫡 saluting salute respect
🤐 zipper mouth secret
🤨 raised eyebrow suspicious
😐 neutral face meh
😑 expressionless blank
😶 no mouth speechless
🫥 dotted line invisible
😏 smirking smug
😒 unamused annoyed
🙄 rolling eyes whatever
😬 grimacing awkward
😮‍💨 exhaling sigh relief
🤥 lying pinocchio
😌 relieved calm
😔 pensive sad
😪 sleepy tired
🤤 drooling
😴 sleeping zzz tired
😷 medical mask sick
🤒 thermometer sick fever
🤕 head bandage hurt
🤢 nauseated sick green
🤮 vomiting sick
🤧 sneezing sick cold
🥵 hot face heat sweating
🥶 cold face freezing
🥴 woozy dizzy drunk
😵 dizzy knocked out
😵‍💫 spiral eyes dizzy confused
🤯 exploding head mind blown
🤠 cowboy hat yeehaw
🥳 partying party celebrate birthday
🥸 disguise glasses
😎 sunglasses cool
🤓 nerd glasses geek
🧐 monocle curious
😕 confused
🫤 diagonal mouth unsure
😟 worried
🙁 slightly frowning sad
☹️ frowning sad
😮 open mouth surprised
😯 hushed surprised
😲 astonished shocked
😳 flushed embarrassed blush
🥺 pleading puppy eyes please
🥹 holding back tears touched
😦 frowning open mouth
😧 anguished
😨 fearful scared
😰 anxious sweat
😥 sad relieved disappointed
😢 crying tear sad
😭 loudly crying sob sad
😱 screaming fear horror
😖 confounded
😣 persevering
😞 disappointed
😓 downcast sweat
😩 weary tired
😫 tired exhausted
🥱 yawning bored tired
😤 steam nose triumph huff
😡 pouting angry rage
😠 angry mad
🤬 cursing swearing angry
😈 smiling devil horns evil
👿 angry devil imp
💀 skull dead dying lmao
☠️ skull crossbones danger
💩 poop
🤡 clown
👹 ogre oni
👺 goblin tengu
👻 ghost boo halloween
👽 alien ufo
👾 space invader game alien
🤖 robot bot
😺 grinning cat
😸 grinning cat smiling eyes
😹 cat tears of joy
😻 cat heart eyes
😼 cat wry smile
😽 kissing cat
🙀 weary cat shocked
😿 crying cat
😾 pouting cat
🙈 see no evil monkey
🙉 hear no evil monkey
🙊 speak no evil monkey
💋 kiss mark lips
💌 love letter
💘 heart arrow cupid
💝 heart ribbon gift
💖 sparkling heart
💗 growing heart
💓 beating heart
💞 revolving hearts
💕 two hearts love
💟 heart decoration
❣️ heart exclamation
💔 broken heart
❤️‍🔥 heart on fire
❤️‍🩹 mending heart
❤️ red heart love
🧡 orange heart
💛 yellow heart
💚 green heart
💙 blue heart
🩵 light blue heart
💜 purple heart
🤎 brown heart
🖤 black heart
🩶 grey heart
🤍 white heart
🩷 pink heart
💯 hundred points perfect
💢 anger symbol
💥 collision boom
💫 dizzy stars
💦 sweat droplets
💨 dashing away wind
🕳️ hole
💬 speech balloon chat
👁️‍🗨️ eye speech bubble
🗨️ left speech bubble
🗯️ anger bubble
💭 thought balloon thinking
💤 zzz sleeping`
        },
        {
            id: "people", name: "People", icon: "fa-hand", items: `
👋 waving hand hello hi bye
🤚 raised back of hand
🖐️ hand fingers splayed
✋ raised hand stop high five
🖖 vulcan salute
🫱 rightwards hand
🫲 leftwards hand
👌 ok hand perfect
🤌 pinched fingers italian
🤏 pinching small
✌️ victory peace
🤞 crossed fingers luck
🫰 hand index thumb crossed money love
🤟 love you gesture
🤘 rock on horns metal
🤙 call me shaka
👈 pointing left
👉 pointing right
👆 pointing up
🖕 middle finger
👇 pointing down
☝️ index pointing up
🫵 pointing at you
👍 thumbs up yes like
👎 thumbs down no dislike
✊ raised fist
👊 fist bump punch
🤛 left fist
🤜 right fist
👏 clapping applause
🙌 raising hands celebrate
🫶 heart hands love
👐 open hands
🤲 palms up
🤝 handshake deal
🙏 folded hands please thanks pray
✍️ writing hand
💅 nail polish sassy
🤳 selfie
💪 flexed biceps strong
🦾 mechanical arm
🦵 leg
🦶 foot
👂 ear listen
🦻 ear hearing aid
👃 nose smell
🧠 brain smart
🫀 anatomical heart
🫁 lungs
🦷 tooth
🦴 bone
👀 eyes looking
👁️ eye
👅 tongue
👄 mouth lips
🫦 biting lip
👶 baby
🧒 child
👦 boy
👧 girl
🧑 person
👱 blond person
👨 man
🧔 bearded person
👩 woman
🧓 older person
👴 old man
👵 old woman
🙍 person frowning
🙎 person pouting
🙅 person gesturing no
🙆 person gesturing ok
💁 person tipping hand
🙋 person raising hand
🧏 deaf person
🙇 person bowing sorry
🤦 facepalm
🤷 shrug idk
🧑‍💻 technologist coder programmer
🧑‍🎤 singer
🧑‍🎨 artist
🧑‍🚀 astronaut
🧑‍🍳 cook chef
🧑‍🎓 student graduate
🥷 ninja
🦸 superhero
🦹 supervillain
🧙 mage wizard witch
🧚 fairy
🧛 vampire
🧜 merperson mermaid
🧝 elf
🧞 genie
🧟 zombie
💆 massage
💇 haircut
🚶 walking
🏃 running
💃 dancing woman dance
🕺 dancing man dance
👯 people bunny ears dancing
🧘 yoga meditation
🛀 bath
🛌 in bed sleeping
👭 women holding hands
👫 couple holding hands
👬 men holding hands
💏 kiss couple
💑 couple heart
👪 family
🗣️ speaking head talking
👤 bust silhouette
👥 busts silhouette group
🫂 people hugging hug
👣 footprints`
        },
        {
            id: "nature", name: "Animals & nature", icon: "fa-leaf", items: `
🐵 monkey face
🐒 monkey
🦍 gorilla
🐶 dog face puppy
🐕 dog
🦮 guide dog
🐩 poodle
🐺 wolf
🦊 fox
🦝 raccoon
🐱 cat face kitty
🐈 cat
🐈‍⬛ black cat
🦁 lion
🐯 tiger face
🐅 tiger
🐆 leopard
🐴 horse face
🦄 unicorn
🦓 zebra
🦌 deer
🐮 cow face
🐂 ox
🐷 pig face
🐖 pig
🐗 boar
🐑 sheep
🐐 goat
🐪 camel
🦒 giraffe
🐘 elephant
🦏 rhino
🦛 hippo
🐭 mouse face
🐁 mouse
🐀 rat
🐹 hamster
🐰 rabbit face bunny
🐇 rabbit bunny
🐿️ chipmunk squirrel
🦔 hedgehog
🦇 bat
🐻 bear
🐻‍❄️ polar bear
🐨 koala
🐼 panda
🦥 sloth
🦦 otter
🦨 skunk
🦘 kangaroo
🦡 badger
🐾 paw prints
🦃 turkey
🐔 chicken
🐓 rooster
🐣 hatching chick
🐤 baby chick
🐥 front facing chick
🐦 bird
🐧 penguin
🕊️ dove peace
🦅 eagle
🦆 duck
🦢 swan
🦉 owl
🦩 flamingo
🦚 peacock
🦜 parrot
🐸 frog
🐊 crocodile
🐢 turtle
🦎 lizard
🐍 snake
🐲 dragon face
🐉 dragon
🦕 dinosaur sauropod
🦖 t rex dinosaur
🐳 spouting whale
🐋 whale
🐬 dolphin
🦭 seal
🐟 fish
🐠 tropical fish
🐡 blowfish
🦈 shark
🐙 octopus
🐚 shell
🪸 coral
🐌 snail
🦋 butterfly
🐛 bug caterpillar
🐜 ant
🐝 bee honeybee
🪲 beetle
🐞 lady beetle ladybug
🦗 cricket
🕷️ spider
🕸️ spider web
🦂 scorpion
💐 bouquet flowers
🌸 cherry blossom sakura flower
💮 white flower
🪷 lotus
🏵️ rosette
🌹 rose flower
🥀 wilted flower
🌺 hibiscus flower
🌻 sunflower
🌼 blossom flower
🌷 tulip flower
🪻 hyacinth
🌱 seedling sprout plant
🪴 potted plant
🌲 evergreen tree
🌳 deciduous tree
🌴 palm tree
🌵 cactus
🌾 rice sheaf
🌿 herb leaf
☘️ shamrock
🍀 four leaf clover luck
🍁 maple leaf autumn
🍂 fallen leaves autumn
🍃 leaves wind
🍄 mushroom
🌍 globe europe africa earth
🌎 globe americas earth
🌏 globe asia australia earth
🌑 new moon
🌒 waxing crescent moon
🌓 first quarter moon
🌔 waxing gibbous moon
🌕 full moon
🌖 waning gibbous moon
🌗 last quarter moon
🌘 waning crescent moon
🌙 crescent moon night
🌚 new moon face
🌛 first quarter moon face
🌜 last quarter moon face
☀️ sun sunny
🌝 full moon face
🌞 sun with face
🪐 ringed planet saturn
⭐ star
🌟 glowing star
🌠 shooting star
🌌 milky way galaxy
☁️ cloud
⛅ sun behind cloud
⛈️ cloud lightning rain storm
🌤️ sun small cloud
🌥️ sun large cloud
🌦️ sun rain cloud
🌧️ cloud rain
🌨️ cloud snow
🌩️ cloud lightning
🌪️ tornado
🌫️ fog
🌬️ wind face
🌀 cyclone
🌈 rainbow
☂️ umbrella
☔ umbrella rain
⚡ high voltage lightning zap
❄️ snowflake winter cold
☃️ snowman
⛄ snowman without snow
☄️ comet
🔥 fire lit hot
💧 droplet water
🌊 water wave ocean`
        },
        {
            id: "food", name: "Food & drink", icon: "fa-mug-hot", items: `
🍇 grapes
🍈 melon
🍉 watermelon
🍊 tangerine orange
🍋 lemon
🍌 banana
🍍 pineapple
🥭 mango
🍎 red apple
🍏 green apple
🍐 pear
🍑 peach
🍒 cherries
🍓 strawberry
🫐 blueberries
🥝 kiwi
🍅 tomato
🥥 coconut
🥑 avocado
🍆 eggplant aubergine
🥔 potato
🥕 carrot
🌽 corn
🌶️ hot pepper spicy
🥒 cucumber
🥬 leafy green
🥦 broccoli
🧄 garlic
🧅 onion
🥜 peanuts
🌰 chestnut
🍞 bread
🥐 croissant
🥖 baguette
🥨 pretzel
🥯 bagel
🥞 pancakes
🧇 waffle
🧀 cheese
🍖 meat on bone
🍗 poultry leg chicken
🥩 cut of meat steak
🥓 bacon
🍔 hamburger burger
🍟 french fries
🍕 pizza
🌭 hot dog
🥪 sandwich
🌮 taco
🌯 burrito
🥙 stuffed flatbread kebab
🧆 falafel
🥚 egg
🍳 cooking fried egg
🥘 shallow pan of food paella
🍲 pot of food stew
🥣 bowl with spoon cereal
🥗 green salad
🍿 popcorn movie
🧈 butter
🧂 salt
🥫 canned food
🍱 bento box
🍘 rice cracker
🍙 rice ball onigiri
🍚 cooked rice
🍛 curry rice
🍜 steaming bowl ramen noodles
🍝 spaghetti pasta
🍠 roasted sweet potato
🍢 oden
🍣 sushi
🍤 fried shrimp tempura
🍥 fish cake
🥮 moon cake
🍡 dango
🥟 dumpling
🥠 fortune cookie
🥡 takeout box
🦀 crab
🦞 lobster
🦐 shrimp
🦑 squid
🍦 soft ice cream
🍧 shaved ice
🍨 ice cream
🍩 doughnut donut
🍪 cookie
🎂 birthday cake
🍰 shortcake cake slice
🧁 cupcake
🥧 pie
🍫 chocolate bar
🍬 candy sweet
🍭 lollipop
🍮 custard pudding flan
🍯 honey pot
🍼 baby bottle milk
🥛 glass of milk
☕ hot beverage coffee tea
🫖 teapot
🍵 teacup matcha green tea
🍶 sake
🍾 champagne bottle celebrate
🍷 wine glass
🍸 cocktail glass martini
🍹 tropical drink
🍺 beer mug
🍻 clinking beer mugs cheers
🥂 clinking glasses cheers toast
🥃 tumbler whiskey
🥤 cup with straw soda
🧋 bubble tea boba
🧃 juice box
🧉 mate
🧊 ice cube
🥢 chopsticks
🍽️ fork knife plate dinner
🍴 fork and knife
🥄 spoon`
        },
        {
            id: "activity", name: "Activities", icon: "fa-futbol", items: `
🎃 jack o lantern pumpkin halloween
🎄 christmas tree
🎆 fireworks
🎇 sparkler
🧨 firecracker
✨ sparkles shiny
🎈 balloon party
🎉 party popper tada celebrate
🎊 confetti ball
🎋 tanabata tree
🎍 pine decoration
🎎 japanese dolls
🎏 carp streamer
🎐 wind chime
🎑 moon viewing
🧧 red envelope
🎀 ribbon bow
🎁 wrapped gift present
🎗️ reminder ribbon
🎟️ admission tickets
🎫 ticket
🎖️ military medal
🏆 trophy win
🏅 sports medal
🥇 first place gold medal
🥈 second place silver medal
🥉 third place bronze medal
⚽ soccer football
⚾ baseball
🥎 softball
🏀 basketball
🏐 volleyball
🏈 american football
🏉 rugby
🎾 tennis
🥏 flying disc frisbee
🎳 bowling
🏏 cricket game
🏑 field hockey
🏒 ice hockey
🥍 lacrosse
🏓 ping pong table tennis
🏸 badminton
🥊 boxing glove
🥋 martial arts
🥅 goal net
⛳ golf flag
⛸️ ice skate
🎣 fishing
🤿 diving mask
🎽 running shirt
🎿 skis
🛷 sled
🥌 curling stone
🎯 bullseye target dart
🪀 yo yo
🪁 kite
🎱 pool 8 ball
🔮 crystal ball magic
🪄 magic wand
🎮 video game controller gaming
🕹️ joystick arcade
🎰 slot machine
🎲 game die dice
🧩 puzzle piece
🧸 teddy bear
🪅 pinata
🪩 mirror ball disco
🪆 nesting dolls
♠️ spade suit
♥️ heart suit
♦️ diamond suit
♣️ club suit
♟️ chess pawn
🃏 joker
🀄 mahjong
🎴 flower playing cards
🎭 performing arts theater masks
🖼️ framed picture
🎨 artist palette art paint
🧵 thread
🧶 yarn knitting
🎤 microphone karaoke sing
🎧 headphones music
🎼 musical score
🎵 musical note music
🎶 musical notes music
🎷 saxophone
🎸 guitar
🎹 piano keyboard
🎺 trumpet
🎻 violin
🥁 drum
🪘 long drum
📻 radio
🎬 clapper board movie film
🎥 movie camera
📺 television tv
📷 camera photo
📸 camera flash photo`
        },
        {
            id: "travel", name: "Travel & places", icon: "fa-plane", items: `
🗺️ world map
🧭 compass
🏔️ snow capped mountain
⛰️ mountain
🌋 volcano
🗻 mount fuji
🏕️ camping
🏖️ beach umbrella
🏜️ desert
🏝️ desert island
🏞️ national park
🏟️ stadium
🏛️ classical building
🏗️ construction
🏘️ houses
🏠 house home
🏡 house with garden
🏢 office building
🏥 hospital
🏦 bank
🏨 hotel
🏪 convenience store
🏫 school
🏰 castle
🏯 japanese castle
💒 wedding
🗼 tokyo tower
🗽 statue of liberty
⛪ church
🕌 mosque
⛩️ shinto shrine torii
🕋 kaaba
⛲ fountain
⛺ tent
🌁 foggy
🌃 night with stars city
🏙️ cityscape
🌄 sunrise over mountains
🌅 sunrise
🌆 cityscape at dusk
🌇 sunset
🌉 bridge at night
🎠 carousel horse
🎡 ferris wheel
🎢 roller coaster
🎪 circus tent
🚂 locomotive train
🚃 railway car
🚄 high speed train
🚅 bullet train shinkansen
🚇 metro subway
🚉 station
🚌 bus
🚑 ambulance
🚒 fire engine
🚓 police car
🚕 taxi
🚗 automobile car
🚙 suv
🛻 pickup truck
🚚 delivery truck
🏎️ racing car
🏍️ motorcycle
🛵 motor scooter
🚲 bicycle bike
🛴 kick scooter
🛹 skateboard
🛼 roller skate
⛽ fuel pump gas
🚨 police light siren
🚥 traffic light
🚧 construction sign
⚓ anchor
⛵ sailboat
🚤 speedboat
🛳️ passenger ship cruise
🚢 ship
✈️ airplane plane flight
🛫 airplane departure
🛬 airplane arrival
🪂 parachute
💺 seat
🚁 helicopter
🚀 rocket launch
🛸 flying saucer ufo
🛰️ satellite
🧳 luggage travel
⌛ hourglass done
⏳ hourglass flowing
⌚ watch
⏰ alarm clock
⏱️ stopwatch
⏲️ timer clock
🕰️ mantelpiece clock
🌡️ thermometer temperature`
        },
        {
            id: "objects", name: "Objects", icon: "fa-lightbulb", items: `
👓 glasses
🕶️ sunglasses
🥽 goggles
👔 necktie
👕 t shirt
👖 jeans
🧣 scarf
🧤 gloves
🧥 coat
🧦 socks
👗 dress
👘 kimono
👙 bikini
👛 purse
👜 handbag
🎒 backpack school
👞 shoe
👟 sneaker running shoe
👠 high heel
👢 boot
👑 crown king queen
👒 hat
🎩 top hat
🎓 graduation cap
🧢 billed cap
⛑️ rescue helmet
💄 lipstick
💍 ring wedding
💎 gem diamond
🔇 muted speaker
🔈 speaker low
🔉 speaker medium
🔊 speaker loud
📢 loudspeaker
📣 megaphone
🔔 bell notification
🔕 bell muted
📱 mobile phone
☎️ telephone
📞 telephone receiver call
🔋 battery
🪫 low battery
🔌 electric plug
💻 laptop computer
🖥️ desktop computer
🖨️ printer
⌨️ keyboard
🖱️ computer mouse
💽 computer disk
💾 floppy disk save
💿 optical disk cd
📀 dvd
🎞️ film frames
📽️ film projector
🔍 magnifying glass search
💡 light bulb idea
🔦 flashlight
🏮 red paper lantern
🕯️ candle
📔 notebook
📕 closed book
📖 open book reading
📚 books study
📓 notebook
📝 memo note writing
✏️ pencil
✒️ black nib pen
🖊️ pen
🖌️ paintbrush
🖍️ crayon
📁 file folder
📅 calendar
📆 tear off calendar
📌 pushpin
📍 round pushpin location
📎 paperclip
✂️ scissors
🔒 locked
🔓 unlocked
🔑 key
🗝️ old key
🔨 hammer
⚔️ crossed swords
🗡️ dagger
🛡️ shield
🏹 bow and arrow
🔧 wrench
🪛 screwdriver
⚙️ gear settings
🧲 magnet
⚗️ alembic
🧪 test tube science
🔬 microscope
🔭 telescope
📡 satellite antenna
💉 syringe
💊 pill
🩹 adhesive bandage
🚪 door
🛏️ bed
🛋️ couch
🚽 toilet
🛁 bathtub
🧸 teddy bear
🛒 shopping cart
🚬 cigarette
⚰️ coffin
🗿 moai
💰 money bag
🪙 coin
💸 money with wings
💳 credit card
✉️ envelope
📧 e mail
📦 package box
🏷️ label tag
📜 scroll
📰 newspaper
📊 bar chart
📈 chart increasing
📉 chart decreasing
🗑️ wastebasket trash
⏰ alarm clock
🎙️ studio microphone podcast
🎚️ level slider
🎛️ control knobs
🧿 nazar amulet
🪬 hamsa
📿 prayer beads`
        },
        {
            id: "symbols", name: "Symbols", icon: "fa-heart", items: `
💟 heart decoration
☮️ peace symbol
✝️ latin cross
☪️ star and crescent
🕉️ om
☸️ wheel of dharma
✡️ star of david
🔯 six pointed star
☯️ yin yang
☦️ orthodox cross
♈ aries zodiac
♉ taurus zodiac
♊ gemini zodiac
♋ cancer zodiac
♌ leo zodiac
♍ virgo zodiac
♎ libra zodiac
♏ scorpio zodiac
♐ sagittarius zodiac
♑ capricorn zodiac
♒ aquarius zodiac
♓ pisces zodiac
⛎ ophiuchus
🔀 shuffle
🔁 repeat
🔂 repeat single
▶️ play
⏩ fast forward
⏭️ next track
⏯️ play pause
◀️ reverse
⏪ rewind
⏮️ previous track
🔼 up button
⏫ fast up
🔽 down button
⏬ fast down
⏸️ pause
⏹️ stop
⏺️ record
🎦 cinema
🔅 dim
🔆 bright
📶 signal bars antenna
📳 vibration mode
📴 phone off
♀️ female sign
♂️ male sign
⚧️ transgender symbol
✖️ multiply
➕ plus
➖ minus
➗ divide
🟰 equals
♾️ infinity
‼️ double exclamation
⁉️ exclamation question
❓ question mark
❔ white question mark
❕ white exclamation mark
❗ exclamation mark
〰️ wavy dash
💱 currency exchange
💲 dollar sign
⚕️ medical symbol
♻️ recycling
⚜️ fleur de lis
🔱 trident
📛 name badge
🔰 beginner
⭕ hollow red circle
✅ check mark button done yes
☑️ check box
✔️ check mark
❌ cross mark no
❎ cross mark button
➰ curly loop
➿ double curly loop
〽️ part alternation mark
✳️ eight spoked asterisk
✴️ eight pointed star
❇️ sparkle
©️ copyright
®️ registered
™️ trade mark
#️⃣ keycap hash number
*️⃣ keycap asterisk
0️⃣ keycap zero
1️⃣ keycap one
2️⃣ keycap two
3️⃣ keycap three
4️⃣ keycap four
5️⃣ keycap five
6️⃣ keycap six
7️⃣ keycap seven
8️⃣ keycap eight
9️⃣ keycap nine
🔟 keycap ten
🆒 cool button
🆓 free button
🆕 new button
🆗 ok button
🆘 sos help
🆙 up button
🆚 vs versus
🈁 japanese here button
🉐 japanese bargain
㊗️ japanese congratulations
㊙️ japanese secret
🔴 red circle
🟠 orange circle
🟡 yellow circle
🟢 green circle
🔵 blue circle
🟣 purple circle
🟤 brown circle
⚫ black circle
⚪ white circle
🟥 red square
🟧 orange square
🟨 yellow square
🟩 green square
🟦 blue square
🟪 purple square
🟫 brown square
⬛ black large square
⬜ white large square
🔶 large orange diamond
🔷 large blue diamond
🔸 small orange diamond
🔹 small blue diamond
🔺 red triangle up
🔻 red triangle down
💠 diamond with a dot
🔘 radio button
🏳️ white flag
🏴 black flag
🏁 chequered flag finish race
🚩 triangular flag
🏳️‍🌈 rainbow flag pride
🏳️‍⚧️ transgender flag pride
🏴‍☠️ pirate flag`
        }
    ];

    // Plain text symbols: these come out the same in any font, which suits
    // VRChat's chatbox better than coloured emoji.
    const symbols = [
        {
            name: "Hearts & stars", items: `
♡ white heart outline love
♥ black heart love
❤ heavy heart love
❥ rotated heart bullet
❦ floral heart
❧ rotated floral heart
ღ georgian heart love
☆ white star outline
★ black star
✦ four pointed star sparkle
✧ four pointed star outline sparkle
✩ stress outlined star
✪ circled star
✫ open centre star
✬ black centre star
✭ outlined black star
✮ heavy outlined star
✯ pinwheel star
✰ shadowed star
⋆ star operator small
✶ six pointed star
✷ eight pointed star
✸ heavy eight star
✹ twelve pointed star
❂ circled sun star
⁂ asterism
※ reference mark
✺ sixteen pointed star
❋ propeller asterisk
✿ black florette flower
❀ white florette flower
❁ eight petalled flower
✾ six petalled flower
❃ teardrop flower
❊ eight teardrop asterisk
✽ heavy teardrop asterisk
❄ snowflake
❅ tight snowflake
❆ heavy snowflake
✼ open centre asterisk`
        },
        {
            name: "Moon, sun & weather", items: `
☾ last quarter moon crescent
☽ first quarter moon crescent
☼ sun with rays
☀ black sun
☁ cloud
☂ umbrella
☃ snowman
☄ comet
⛅ sun behind cloud
⚡ lightning
☇ lightning
☈ thunderstorm
✺ sun burst
❂ sun circle`
        },
        {
            name: "Music & play", items: `
♪ eighth note music
♫ beamed eighth notes music
♬ beamed sixteenth notes music
♩ quarter note music
♭ flat music
♮ natural music
♯ sharp music
𝄞 g clef treble music
▶ play triangle
◀ reverse triangle
⏸ pause
⏹ stop
⏺ record
⏭ next track
⏮ previous track
⏯ play pause
🔁︎ repeat
⟲ anticlockwise loop
⟳ clockwise loop`
        },
        {
            name: "Arrows", items: `
→ right arrow
← left arrow
↑ up arrow
↓ down arrow
↔ left right arrow
↕ up down arrow
↗ north east arrow
↘ south east arrow
↙ south west arrow
↖ north west arrow
⇒ double right arrow
⇐ double left arrow
⇑ double up arrow
⇓ double down arrow
⇔ double left right arrow
➜ heavy round arrow right
➔ heavy wide arrow right
➤ arrowhead right
➢ 3d arrowhead right
➳ white feathered arrow right
➵ black feathered arrow right
↩ return arrow left hook
↪ return arrow right hook
⤴ arrow curving up
⤵ arrow curving down
↻ clockwise arrow refresh
↺ anticlockwise arrow
⟶ long right arrow
⟵ long left arrow
» right guillemet
« left guillemet
› right angle quote
‹ left angle quote
⋙ very much greater
⋘ very much less`
        },
        {
            name: "Shapes & bullets", items: `
• bullet dot
◦ white bullet
‣ triangular bullet
⁃ hyphen bullet
∙ bullet operator
· middle dot
○ white circle
● black circle
◎ bullseye
◉ fisheye
◌ dotted circle
◍ circle vertical fill
◐ circle left half black
◑ circle right half black
□ white square
■ black square
▢ rounded square
▣ square inside square
▪ small black square
▫ small white square
◇ white diamond
◆ black diamond
◈ diamond inside diamond
◊ lozenge
△ white up triangle
▲ black up triangle
▽ white down triangle
▼ black down triangle
▷ white right triangle
◁ white left triangle
▸ small right triangle
◂ small left triangle
⬡ white hexagon
⬢ black hexagon
⌂ house
⌘ command place of interest
☐ ballot box empty checkbox
☑ ballot box check checkbox
☒ ballot box x checkbox`
        },
        {
            name: "Lines & brackets", items: `
「 left corner bracket japanese
」 right corner bracket japanese
『 left white corner bracket
』 right white corner bracket
【 left lenticular bracket
】 right lenticular bracket
〔 left tortoise shell bracket
〕 right tortoise shell bracket
〈 left angle bracket
〉 right angle bracket
《 left double angle bracket
》 right double angle bracket
〖 left white lenticular bracket
〗 right white lenticular bracket
｢ halfwidth left corner bracket
｣ halfwidth right corner bracket
⟨ mathematical left angle
⟩ mathematical right angle
─ box light horizontal line
━ box heavy horizontal line
│ box light vertical line
┃ box heavy vertical line
┄ dashed line
┈ dotted line
═ double horizontal line
║ double vertical line
╭ rounded corner down right
╮ rounded corner down left
╰ rounded corner up right
╯ rounded corner up left
┌ corner down right
┐ corner down left
└ corner up right
┘ corner up left
├ tee right
┤ tee left
⎯ horizontal line extension
〜 wave dash
～ fullwidth tilde
﹏ wavy low line
︶ smile bracket
︵ frown bracket
｜ fullwidth vertical bar
⋮ vertical ellipsis
… ellipsis dots
‥ two dot leader
⸻ three em dash
— em dash
– en dash`
        },
        {
            name: "Checks & signs", items: `
✓ check mark tick yes
✔ heavy check mark tick
✗ ballot x cross no
✘ heavy ballot x cross
☓ saltire cross
✕ multiplication x
✖ heavy multiplication x
⚠ warning sign
⛔ no entry
☢ radioactive
☣ biohazard
☮ peace
☯ yin yang
☠ skull and crossbones
⚓ anchor
⚔ crossed swords
⚖ scales justice
⚙ gear
⚛ atom
⚜ fleur de lis
✉ envelope mail
✎ pencil
✂ scissors
☎ telephone
⌚ watch
⌛ hourglass
⏳ hourglass flowing
☕ coffee
♿ wheelchair
♻ recycle
⚐ white flag
⚑ black flag
❗ exclamation
❓ question
‼ double exclamation
⁉ exclamation question
¡ inverted exclamation
¿ inverted question`
        },
        {
            name: "Math & money", items: `
± plus minus
× times multiply
÷ divide
≈ almost equal
≠ not equal
≤ less or equal
≥ greater or equal
∞ infinity
√ square root
∑ sum sigma
∏ product pi
π pi
∆ delta increment
∂ partial
∫ integral
∴ therefore
∵ because
∈ element of
∀ for all
∃ there exists
∅ empty set
∩ intersection
∪ union
° degree
℃ degree celsius
℉ degree fahrenheit
‰ per mille
µ micro
² superscript two squared
³ superscript three cubed
½ one half
¼ one quarter
¾ three quarters
€ euro
£ pound
¥ yen yuan
₩ won
₹ rupee
₽ ruble
¢ cent
§ section
¶ pilcrow paragraph
© copyright
® registered
™ trademark
№ numero number`
        },
        {
            name: "Greek & letters", items: `
α alpha
β beta
γ gamma
δ delta
ε epsilon
ζ zeta
η eta
θ theta
λ lambda
μ mu
ξ xi
σ sigma
τ tau
φ phi
χ chi
ψ psi
ω omega
Ω omega capital
ß sharp s
ø o slash
æ ae
œ oe
ð eth
þ thorn
ℓ script l
ℵ aleph
☊ ascending node
☋ descending node
ᐛ canadian syllabics pa
ツ katakana tsu smile
シ katakana shi
ッ small tsu
の japanese no
ω omega cat mouth`
        }
    ];

    const kaomoji = [
        {
            name: "Happy", items: [
                ["(＾▽＾)", "happy smile"],
                ["(◕‿◕)", "happy smile cute"],
                ["(｡◕‿◕｡)", "happy smile cute"],
                ["(✿◠‿◠)", "happy flower smile"],
                ["(´▽`)", "happy smile"],
                ["ヽ(・∀・)ﾉ", "happy cheer yay"],
                ["(ﾉ◕ヮ◕)ﾉ*:･ﾟ✧", "happy excited sparkle yay"],
                ["\\(^o^)/", "happy yay cheer"],
                ["٩(◕‿◕｡)۶", "happy yay"],
                ["(* ^ ω ^)", "happy content"],
                ["(≧▽≦)", "happy excited laugh"],
                ["(￣▽￣)", "smug happy grin"],
                ["(^_^)", "smile happy"],
                ["(^▽^)", "smile happy"],
                ["☆*:.｡.o(≧▽≦)o.｡.:*☆", "excited happy sparkle"],
                ["ヾ(≧▽≦*)o", "excited happy"],
                ["(*≧ω≦*)", "excited cute"],
                ["( ˘ ³˘)", "content kiss"]
            ]
        },
        {
            name: "Love", items: [
                ["(♡°▽°♡)", "love heart"],
                ["(´｡• ᵕ •｡`) ♡", "love shy heart"],
                ["(っ˘з(˘⌣˘ ) ♡", "love kiss couple"],
                ["(｡♥‿♥｡)", "love heart eyes"],
                ["♡(˃͈ દ ˂͈ ༶ )", "love shy"],
                ["(づ｡◕‿‿◕｡)づ", "hug love"],
                ["(つ≧▽≦)つ", "hug"],
                ["⊂(・▽・⊂)", "hug"],
                ["(っ´▽`)っ", "hug"],
                ["(˘∀˘)/(μ‿μ) ❤", "love couple"],
                ["(*˘︶˘*).｡.:*♡", "love content"],
                ["(灬º‿º灬)♡", "love blush"]
            ]
        },
        {
            name: "Sad", items: [
                ["(╥﹏╥)", "sad crying"],
                ["(ಥ﹏ಥ)", "sad crying"],
                ["(T_T)", "sad crying"],
                ["(;_;)", "sad crying"],
                ["(｡•́︿•̀｡)", "sad pout"],
                ["(´；ω；`)", "sad crying"],
                ["(っ˘̩╭╮˘̩)っ", "sad"],
                ["(︶︹︺)", "sad"],
                ["(ノ_<。)", "sad crying"],
                ["o(╥﹏╥)o", "sad crying"],
                ["(｡╯︵╰｡)", "sad"]
            ]
        },
        {
            name: "Angry", items: [
                ["(╯°□°)╯︵ ┻━┻", "table flip angry rage"],
                ["┬─┬ノ( º _ ºノ)", "table put back calm"],
                ["(ノಠ益ಠ)ノ彡┻━┻", "table flip angry"],
                ["(｀Д´)", "angry"],
                ["(＃`Д´)", "angry"],
                ["(¬_¬)", "annoyed side eye"],
                ["ಠ_ಠ", "disapproval look"],
                ["(눈_눈)", "annoyed stare"],
                ["(╬ Ò﹏Ó)", "angry"],
                ["ლ(ಠ益ಠლ)", "angry why"]
            ]
        },
        {
            name: "Surprised", items: [
                ["(⊙_⊙)", "surprised shocked"],
                ["(°ロ°)", "surprised shocked"],
                ["(O_O)", "surprised"],
                ["Σ(°△°|||)", "shocked"],
                ["(゜゜)", "surprised"],
                ["w(°ｏ°)w", "surprised wow"],
                ["(✪ω✪)", "amazed sparkle"],
                ["(・□・;)", "nervous surprised"]
            ]
        },
        {
            name: "Greetings", items: [
                ["(^_^)/", "hi wave hello"],
                ["(￣▽￣)ノ", "hi wave hello"],
                ["ヾ(・ω・*)", "hi wave bye"],
                ["(｡･ω･)ﾉﾞ", "hi wave bye"],
                ["( ´ ▽ ` )ﾉ", "hi wave"],
                ["(*・ω・)ﾉ", "hi wave"],
                ["(￣^￣)ゞ", "salute"],
                ["m(_ _)m", "sorry bow thanks"],
                ["(シ_ _)シ", "bow sorry"],
                ["(o´▽`o)ﾉ", "bye wave"]
            ]
        },
        {
            name: "Animals", items: [
                ["(=^･ω･^=)", "cat"],
                ["(=①ω①=)", "cat"],
                ["ฅ^•ﻌ•^ฅ", "cat paws"],
                ["/ᐠ｡ꞈ｡ᐟ\\", "cat"],
                ["(^・ω・^ )", "cat"],
                ["U・ᴥ・U", "dog"],
                ["V●ᴥ●V", "dog"],
                ["(・(ｪ)・)", "bear"],
                ["ʕ•ᴥ•ʔ", "bear"],
                ["ʕ っ•ᴥ•ʔっ", "bear hug"],
                ["(\\(•ᴗ•)/)", "bunny rabbit"],
                ["(￣(ｴ)￣)", "bear"],
                ["くコ:彡", "squid"],
                ["<・ )))><<", "fish"],
                ["~>°)～～～", "snake"],
                ["(•ө•)", "bird"]
            ]
        },
        {
            name: "Sleepy & misc", items: [
                ["(－_－) zzZ", "sleepy sleep tired"],
                ["(∪｡∪)｡｡｡zzZ", "sleepy sleep"],
                ["(￣o￣) zzZZzzZZ", "sleep snore"],
                ["(っ- ‸ – ς)", "tired"],
                ["¯\\_(ツ)_/¯", "shrug idk whatever"],
                ["┐(￣ヘ￣)┌", "shrug whatever"],
                ["(・_・;)", "nervous awkward sweat"],
                ["(^_^;)", "nervous awkward"],
                ["(￣ω￣;)", "awkward"],
                ["( ͡° ͜ʖ ͡°)", "lenny face smirk"],
                ["(•_•) ( •_•)>⌐■-■ (⌐■_■)", "deal with it sunglasses cool"],
                ["(☞ﾟヮﾟ)☞", "point you"],
                ["☜(ﾟヮﾟ☜)", "point"],
                ["(ง •̀_•́)ง", "fight determined"],
                ["(｀・ω・´)", "determined serious"],
                ["( ˘▽˘)っ♨", "tea hot drink"],
                ["( ˙▿˙ )", "blank"],
                ["(〃＾▽＾〃)", "blush happy"],
                ["(⁄ ⁄•⁄ω⁄•⁄ ⁄)", "shy blush embarrassed"],
                ["(｡•̀ᴗ-)✧", "wink"],
                ["(^_−)☆", "wink star"],
                ["φ(..)", "writing note"],
                ["♪～(￣ε￣)", "whistling music"],
                ["ヾ(⌐■_■)ノ♪", "dance music cool"],
                ["┏(＾0＾)┛", "dance"],
                ["(ﾉ´ヮ`)ﾉ*: ･ﾟ", "magic sparkle"]
            ]
        }
    ];

    // parseLines turns "<char> <words>" lines into { c, w } entries.
    function parseLines(text) {
        const out = [];
        text.split("\n").forEach((line) => {
            line = line.trim();
            if (!line) return;
            const space = line.indexOf(" ");
            const c = space < 0 ? line : line.slice(0, space);
            const w = space < 0 ? "" : line.slice(space + 1);
            out.push({ c: c, w: w.toLowerCase() });
        });
        return out;
    }

    window.FernEmojiData = {
        emoji: emoji.map((cat) => ({ id: cat.id, name: cat.name, icon: cat.icon, items: parseLines(cat.items) })),
        symbols: symbols.map((group) => ({ name: group.name, items: parseLines(group.items) })),
        kaomoji: kaomoji.map((group) => ({ name: group.name, items: group.items.map((p) => ({ c: p[0], w: p[1] })) }))
    };
})();
